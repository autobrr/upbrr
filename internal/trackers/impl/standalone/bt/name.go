// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	return metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename, pathutil.Base(meta.SourcePath))
}

func resolveSearchName(meta api.UploadSubject) string {
	if !meta.Anime {
		return resolveUploadName(meta)
	}
	candidates := make([]string, 0, 6)
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TMDB.Title))
	}
	if meta.ProviderMetadata.IMDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.IMDB.Title))
	}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.Name))
	}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle))
	}
	candidates = append(candidates, strings.TrimSpace(meta.ReleaseName))
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return resolveUploadName(meta)
}
