// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func (s *Service) applyBlurayMetadata(ctx context.Context, meta api.PreparedMetadata, bdinfo *discparse.BDInfo) (api.PreparedMetadata, error) {
	if !s.shouldLookupBluray(meta) {
		meta = applySelectedBlurayCandidate(meta)
		return meta, nil
	}

	imdbID := meta.ExternalIDs.IMDBID
	if imdbID == 0 && meta.ExternalMetadata.IMDB != nil {
		imdbID = meta.ExternalMetadata.IMDB.IMDBID
	}
	if imdbID == 0 {
		return meta, nil
	}

	selectedID := ""
	if meta.ExternalMetadata.Bluray != nil {
		selectedID = strings.TrimSpace(meta.ExternalMetadata.Bluray.SelectedReleaseID)
	}
	if cached := s.reusableBlurayMetadata(meta, imdbID); cached != nil {
		if selectedID != "" {
			cached.SelectCandidate(selectedID, false, "manual")
		}
		meta.ExternalMetadata.Bluray = cached
		meta = applySelectedBlurayCandidate(meta)
		return meta, nil
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
		return meta, nil
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
	meta.ExternalMetadata.Bluray = lookup
	meta.ExternalMetadata.SourcePath = meta.SourcePath
	meta.ExternalMetadata.UpdatedAt = lookup.UpdatedAt
	if err := s.repo.SaveExternalMetadata(ctx, meta.ExternalMetadata); err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: save blu-ray metadata: %w", err)
	}
	meta = applySelectedBlurayCandidate(meta)
	return meta, nil
}

func (s *Service) shouldLookupBluray(meta api.PreparedMetadata) bool {
	if !s.cfg.Metadata.GetBlurayInfo || s.bluray == nil || s.repo == nil {
		return false
	}
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType != "BDMV" && discType != "DVD" {
		return false
	}
	if meta.Options.DryRun {
		return false
	}
	if meta.ExternalIDs.IMDBID == 0 && (meta.ExternalMetadata.IMDB == nil || meta.ExternalMetadata.IMDB.IMDBID == 0) {
		return false
	}
	if meta.ExternalMetadata.Bluray != nil && len(meta.ExternalMetadata.Bluray.Candidates) > 0 {
		return true
	}
	return true
}

func (s *Service) reusableBlurayMetadata(meta api.PreparedMetadata, imdbID int) *api.BlurayMetadata {
	if meta.ExternalMetadata.Bluray == nil {
		return nil
	}
	bluray := *meta.ExternalMetadata.Bluray
	if bluray.IMDBID != imdbID || len(bluray.Candidates) == 0 {
		return nil
	}
	bluray.Candidates = append([]api.BlurayReleaseCandidate(nil), bluray.Candidates...)
	return &bluray
}

func applySelectedBlurayCandidate(meta api.PreparedMetadata) api.PreparedMetadata {
	if meta.ExternalMetadata.Bluray == nil {
		return meta
	}
	candidate := meta.ExternalMetadata.Bluray.SelectedCandidate()
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
