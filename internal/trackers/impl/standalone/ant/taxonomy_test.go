// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveTagsAndAdultPreferManualGenres(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release:          api.ReleaseInfo{Genre: "Porn"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Action, Porn"}},
		EffectiveMetadata: api.EffectiveMetadata{
			Genres: []string{"Drama"}, GenresProvenance: api.FactProvenanceManual,
		},
	}
	if got, manual := resolveTags(meta, nil); got != "drama" || manual {
		t.Fatalf("manual tags = %q, manual = %t", got, manual)
	}
	meta.EffectiveMetadata.Genres = nil
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManualEmpty
	if got, _ := resolveTags(meta, nil); got != "" {
		t.Fatalf("manual-empty tags = %q", got)
	}
	if detectAdult(meta) {
		t.Fatal("manual-empty genres retained provider adult classification")
	}
}
