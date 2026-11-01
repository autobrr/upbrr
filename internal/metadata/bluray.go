// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/logging"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/progress"
	"github.com/autobrr/upbrr/pkg/api"
)

func (s *Service) applyBlurayMetadata(ctx context.Context, meta preparationstate.State, bdinfo *discparse.BDInfo) preparationstate.State {
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
	selectedID := strings.TrimSpace(meta.BlurayReleaseID)
	if selectedID == "" && meta.ProviderMetadata.Bluray != nil && !meta.ProviderMetadata.Bluray.AutoSelected && meta.ProviderMetadata.Bluray.IMDBID == imdbID {
		candidate := meta.ProviderMetadata.Bluray.SelectedCandidate()
		if country == "" || candidate != nil && strings.EqualFold(strings.TrimSpace(candidate.Country), country) {
			selectedID = strings.TrimSpace(meta.ProviderMetadata.Bluray.SelectedReleaseID)
		}
	}
	input.SelectedReleaseID = selectedID
	cached := s.reusableBlurayMetadata(meta, input)
	if cached != nil {
		bluraycom.SelectRelease(cached, input)
	}
	meta.ProviderMetadata.Bluray = cached
	if reason := s.blurayLookupSkipReason(meta); reason != "" {
		logger.Debugf("metadata: blu-ray.com lookup skipped: %s", reason)
		return applySelectedBlurayCandidate(meta)
	}
	if cached != nil && !evidence.Enabled(ctx) {
		step := progress.Begin(ctx, "Read retained Blu-ray.com candidates")
		step.Update(len(cached.Candidates), len(cached.Candidates), "Using retained candidates.")
		step.Finish("Retained candidates ready.", false)
		return applySelectedBlurayCandidate(meta)
	}

	lookup, err := s.bluray.Lookup(ctx, input)
	if err != nil {
		logger.Warnf("metadata: blu-ray.com lookup failed: %v", err)
		if meta.ExternalFreshness.RequiresRefresh() {
			appendProviderRefreshWarning(&meta, "Blu-ray.com", err)
		}
		return applySelectedBlurayCandidate(meta)
	}
	if lookup == nil {
		return meta
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
	return meta
}

func (s *Service) shouldLookupBluray(meta preparationstate.State) bool {
	return s.blurayLookupSkipReason(meta) == ""
}

func (s *Service) blurayLookupSkipReason(meta preparationstate.State) string {
	if !s.blurayLookupEnabled() {
		return "metadata.get_bluray_info and description blu-ray options disabled"
	}
	if s.bluray == nil {
		return "blu-ray.com client unavailable"
	}
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType != "BDMV" && discType != "DVD" {
		return "source is not BDMV/DVD"
	}
	if meta.Identity.IMDBID == 0 && (meta.ProviderMetadata.IMDB == nil || meta.ProviderMetadata.IMDB.IMDBID == 0) {
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
	if bluray.Country != "" && !strings.EqualFold(bluray.Country, input.ReleaseCountry()) {
		return nil
	}
	if input.BDInfo != nil {
		for _, candidate := range bluray.Candidates {
			if slices.Contains(candidate.MatchNotes, "local BDInfo unavailable (-15)") {
				return nil
			}
		}
	}
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
