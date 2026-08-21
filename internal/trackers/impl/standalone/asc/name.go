// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadTitle(meta api.UploadSubject) string {
	base := resolveDisplayTitle(meta)
	if categoryOf(meta) == "TV" {
		seasonEpisode := metautil.FirstNonEmptyTrimmed(
			meta.DailyEpisodeDate,
			strings.TrimSpace(meta.SeasonStr)+strings.TrimSpace(meta.EpisodeStr),
			seasonEpisodeText(meta),
		)
		if seasonEpisode != "" {
			return strings.TrimSpace(base + " - " + seasonEpisode)
		}
	}
	return base
}

func resolveDisplayTitle(meta api.UploadSubject) string {
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
		main := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdb.Title, meta.Release.Title))
		alt := strings.TrimSpace(tmdb.OriginalTitle)
		if categoryOf(meta) == "TV" {
			alt = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(tmdb.Title, meta.Release.Title))
		}
		if meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitAlternateTitle {
			alt = ""
		}
		if main != "" && alt != "" && !strings.EqualFold(main, alt) {
			return main + " (" + alt + ")"
		}
		if main != "" {
			return main
		}
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName, pathutil.Base(meta.SourcePath)))
}

func resolveSearchTitle(meta api.UploadSubject) string {
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.SourcePath)
}

func seasonEpisodeText(meta api.UploadSubject) string {
	if meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return fmt.Sprintf("S%02d", meta.SeasonInt)
	}
	return ""
}
