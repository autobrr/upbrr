// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestGenresTextPrefersManualGenres(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Release:          api.ReleaseInfo{Genre: "Parsed"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Provider"}},
		EffectiveMetadata: api.EffectiveMetadata{
			Genres: []string{"Manual"}, GenresProvenance: api.FactProvenanceManual,
		},
	}
	if got := genresText(meta); got != "Manual" {
		t.Fatalf("manual genres = %q", got)
	}
	meta.EffectiveMetadata.Genres = nil
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManualEmpty
	if got := genresText(meta); got != "" {
		t.Fatalf("manual-empty genres = %q", got)
	}
}
