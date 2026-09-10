// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCategoryIDPrefersManualGenres(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Porn"}},
		EffectiveMetadata: api.EffectiveMetadata{GenresProvenance: api.FactProvenanceManualEmpty},
	}
	if got := categoryID(meta); got != "1" {
		t.Fatalf("manual-empty category = %q", got)
	}
	meta.EffectiveMetadata.Genres = []string{"Documentary"}
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManual
	if got := categoryID(meta); got != "17" {
		t.Fatalf("manual category = %q", got)
	}
}
