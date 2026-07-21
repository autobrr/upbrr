// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"strings"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func (s *Service) applyBlurayMetadata(ctx context.Context, meta preparationstate.State, bdinfo *discparse.BDInfo) preparationstate.State {
	if reason := s.blurayLookupSkipReason(meta); reason != "" {
		if s.logger != nil {
			s.logger.Debugf("metadata: blu-ray.com lookup skipped: %s", reason)
		}
		meta = applySelectedBlurayCandidate(meta)
		return meta
	}

	imdbID := meta.Identity.IMDBID
	if imdbID == 0 && meta.ProviderMetadata.IMDB != nil {
		imdbID = meta.ProviderMetadata.IMDB.IMDBID
	}
	if imdbID == 0 {
		return meta
	}

	selectedID := ""
	if meta.ProviderMetadata.Bluray != nil {
		selectedID = strings.TrimSpace(meta.ProviderMetadata.Bluray.SelectedReleaseID)
	}
	if cached := s.reusableBlurayMetadata(meta, imdbID); cached != nil {
		if selectedID != "" {
			cached.SelectCandidate(selectedID, false, "manual")
		}
		meta.ProviderMetadata.Bluray = cached
		meta = applySelectedBlurayCandidate(meta)
		return meta
	}

	lookup, err := s.bluray.Lookup(ctx, bluraycom.LookupInput{
		SourcePath:        meta.SourcePath,
		IMDBID:            imdbID,
		DiscType:          meta.DiscType,
		Resolution:        meta.Release.Resolution,
		Is3D:              meta.Is3D,
		BDInfo:            bdinfo,
		SelectedReleaseID: selectedID,
		ScoreThreshold:    s.cfg.Metadata.BlurayScore,
		SingleThreshold:   s.cfg.Metadata.BluraySingleScore,
	})
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: blu-ray.com lookup failed: %v", err)
		}
		return meta
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

func (s *Service) reusableBlurayMetadata(meta preparationstate.State, imdbID int) *api.BlurayMetadata {
	if meta.ProviderMetadata.Bluray == nil {
		return nil
	}
	bluray := *meta.ProviderMetadata.Bluray
	if bluray.IMDBID != imdbID || len(bluray.Candidates) == 0 {
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
