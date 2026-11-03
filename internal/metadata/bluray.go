// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/logging"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/pkg/api"
)

func (s *Service) applyBlurayMetadata(ctx context.Context, meta preparationstate.State, bdinfo *discparse.BDInfo) (preparationstate.State, error) {
	logger := logging.FromContext(ctx, s.logger)

	if meta.ExternalFreshness.RequiresRefresh() && !evidence.Enabled(ctx) {
		meta.ProviderMetadata.Bluray = nil
	}
	imdbID := meta.Identity.IMDBID
	if imdbID == 0 && meta.ProviderMetadata.IMDB != nil {
		imdbID = meta.ProviderMetadata.IMDB.IMDBID
	}
	input := bluraycom.LookupInput{
		SourcePath:      meta.SourcePath,
		CountryOverride: meta.ReleaseNameOverrides.Region,
		IMDBID:          imdbID,
		DiscType:        meta.DiscType,
		Resolution:      meta.Release.Resolution,
		Is3D:            meta.Is3D,
		BDInfo:          bdinfo,
		ScoreThreshold:  s.cfg.Metadata.BlurayScore,
		SingleThreshold: s.cfg.Metadata.BluraySingleScore,
	}
	country := input.ReleaseCountry()
	selectedID := ""
	if selection := meta.Bluray; selection != nil {
		switch selection.Mode {
		case "manual":
			selectedID = strings.TrimSpace(selection.Release)
			input.ExactRelease = selectedID
		case "skip":
			input.SkipSelection = true
		case "auto":
		default:
			return meta, fmt.Errorf("metadata: invalid Blu-ray selection mode %q", selection.Mode)
		}
	} else if saved := meta.ProviderMetadata.Bluray; saved != nil && !saved.AutoSelected && saved.IMDBID == imdbID {
		candidate := saved.SelectedCandidate()
		if bluraycom.HasUnverifiedDetails(saved) || country == "" || candidate != nil && strings.EqualFold(strings.TrimSpace(candidate.Country), country) {
			selectedID = saved.SelectedReleaseID
		}
	}
	input.SelectedReleaseID = selectedID
	input.Retained = s.reusableBlurayMetadata(meta, input)
	input.Details = meta.BlurayDetails
	input.FetchSelected = s.cfg.Description.UseBlurayImages && !input.SkipSelection
	if input.Retained != nil && !bluraycom.HasUnverifiedDetails(input.Retained) {
		bluraycom.SelectRelease(input.Retained, input)
	}
	meta.ProviderMetadata.Bluray = input.Retained
	if bluraycom.HasUnverifiedDetails(input.Retained) {
		meta.ProviderMetadata.Bluray = nil
	}
	lookupMeta := meta
	if selectedID != "" {
		lookupMeta.Bluray = &api.BluraySelection{Mode: "manual", Release: selectedID}
	}
	if reason := s.blurayLookupSkipReason(lookupMeta); reason != "" {
		logger.Debugf("metadata: blu-ray.com lookup skipped: %s", reason)
		if meta.BlurayDetails != nil {
			return meta, fmt.Errorf("metadata: Blu-ray inspection unavailable: %s", reason)
		}
		if meta.Bluray != nil && meta.Bluray.Mode == "manual" {
			return meta, fmt.Errorf("metadata: explicit Blu-ray release unavailable: %s", reason)
		}
		return applySelectedBlurayCandidate(meta), nil
	}

	lookup, err := s.bluray.Lookup(ctx, input)
	if err != nil {
		logger.Warnf("metadata: blu-ray.com lookup failed: %v", err)
		if meta.ExternalFreshness.RequiresRefresh() {
			appendProviderRefreshWarning(&meta, "Blu-ray.com", err)
		}
		if errors.Is(err, bluraycom.ErrDetailReleaseNotRetained) {
			return meta, fmt.Errorf("metadata: Blu-ray inspection: %w", err)
		}
		if meta.Bluray != nil && meta.Bluray.Mode == "manual" {
			return meta, fmt.Errorf("metadata: explicit Blu-ray release: %w", err)
		}
		return applySelectedBlurayCandidate(meta), nil
	}
	if lookup == nil {
		return meta, nil
	}
	if strings.TrimSpace(lookup.SourcePath) == "" {
		lookup.SourcePath = meta.SourcePath
	}
	if lookup.UpdatedAt.IsZero() {
		lookup.UpdatedAt = time.Now().UTC()
	}
	merged := meta.ProviderMetadata
	merged.Bluray = lookup
	merged.SourcePath = meta.SourcePath
	merged.UpdatedAt = lookup.UpdatedAt
	meta.ProviderMetadata = merged
	meta = applySelectedBlurayCandidate(meta)
	return meta, nil
}

func (s *Service) shouldLookupBluray(meta preparationstate.State) bool {
	return s.blurayLookupSkipReason(meta) == ""
}

func (s *Service) blurayLookupSkipReason(meta preparationstate.State) string {
	if !s.blurayLookupEnabled() && meta.BlurayDetails == nil && (meta.Bluray == nil || meta.Bluray.Mode != "manual") {
		return "metadata.get_bluray_info and description blu-ray options disabled"
	}
	if s.bluray == nil {
		return "blu-ray.com client unavailable"
	}
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType != "BDMV" && discType != "DVD" {
		return "source is not BDMV/DVD"
	}
	if (meta.Bluray == nil || meta.Bluray.Mode != "manual") && meta.Identity.IMDBID == 0 &&
		(meta.ProviderMetadata.IMDB == nil || meta.ProviderMetadata.IMDB.IMDBID == 0) {
		return "IMDb ID missing"
	}
	if meta.ProviderMetadata.Bluray != nil && len(meta.ProviderMetadata.Bluray.Candidates) > 0 {
		return ""
	}
	return ""
}

func (s *Service) blurayLookupEnabled() bool {
	return s.cfg.Metadata.GetBlurayInfo || s.cfg.Description.AddBlurayLink || s.cfg.Description.UseBlurayImages
}

func (s *Service) reusableBlurayMetadata(meta preparationstate.State, input bluraycom.LookupInput) *api.BlurayMetadata {
	if meta.ProviderMetadata.Bluray == nil {
		return nil
	}
	bluray := *meta.ProviderMetadata.Bluray
	if bluray.IMDBID != input.IMDBID || len(bluray.Candidates) == 0 {
		return nil
	}
	if !bluraycom.HasUnverifiedDetails(&bluray) && bluray.Country != "" && !strings.EqualFold(bluray.Country, input.ReleaseCountry()) {
		return nil
	}
	bluray.Candidates = append([]api.BlurayReleaseCandidate(nil), bluray.Candidates...)

	return &bluray
}

func applySelectedBlurayCandidate(meta preparationstate.State) preparationstate.State {
	if meta.ProviderMetadata.Bluray == nil {
		return meta
	}
	candidate := meta.ProviderMetadata.Bluray.SelectedCandidate()
	if candidate == nil {
		return meta
	}
	if strings.TrimSpace(candidate.Region) != "" {
		meta.Region = strings.TrimSpace(candidate.Region)
		meta.Release.Region = strings.TrimSpace(candidate.Region)
	}
	if strings.TrimSpace(candidate.Publisher) != "" {
		meta.Distributor = strings.ToUpper(strings.TrimSpace(candidate.Publisher))
	}
	return meta
}
