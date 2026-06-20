// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDeepCopyTMDBMetadataLocalizedMap(t *testing.T) {
	t.Parallel()

	source := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 42,
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": {
						Title: "Original Localized Title",
					},
				},
			},
		},
	}

	copied := deepCopyPreparedMetadata(source)

	// Mutate the copied map
	copied.ExternalMetadata.TMDB.Localized["pt-BR"] = api.TMDBLocalizedData{
		Title: "Mutated Localized Title",
	}

	// Verify the source map is unchanged
	origTitle := source.ExternalMetadata.TMDB.Localized["pt-BR"].Title
	if origTitle != "Original Localized Title" {
		t.Fatalf("source map was mutated! expected %q, got %q", "Original Localized Title", origTitle)
	}

	copiedTitle := copied.ExternalMetadata.TMDB.Localized["pt-BR"].Title
	if copiedTitle != "Mutated Localized Title" {
		t.Fatalf("copied map was not mutated! expected %q, got %q", "Mutated Localized Title", copiedTitle)
	}
}
