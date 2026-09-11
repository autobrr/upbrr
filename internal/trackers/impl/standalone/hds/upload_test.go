// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSupportsHDSResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		resolution string
		expected   bool
	}{
		{
			name:       "2160p",
			resolution: "2160p",
			expected:   true,
		},
		{
			name:       "1080i",
			resolution: "1080i",
			expected:   true,
		},
		{
			name:       "720p",
			resolution: "720p",
			expected:   true,
		},
		{
			name:       "576p",
			resolution: "576p",
			expected:   false,
		},
		{
			name:       "480p",
			resolution: "480p",
			expected:   false,
		},
	}

	for _, tc := range cases {
		if got := supportsHDSResolution(tc.resolution); got != tc.expected {
			t.Fatalf("%s: expected %t, got %t", tc.name, tc.expected, got)
		}
	}
}

func TestResolveGenresPreservesAutomaticProviderPresenceAndManualFacts(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Action"}, IMDB: &api.IMDBMetadata{Genres: "Drama"}}}
	if got := resolveGenres(meta); got != "Action" {
		t.Fatalf("TMDB genres = %q", got)
	}
	meta.ProviderMetadata.TMDB.Genres = " \t"
	if got := resolveGenres(meta); got != "" {
		t.Fatalf("blank TMDB genres = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{Genres: []string{"Comedy"}, GenresProvenance: api.FactProvenanceManual}
	if got := resolveGenres(meta); got != "Comedy" {
		t.Fatalf("manual genres = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{GenresProvenance: api.FactProvenanceManualEmpty}
	if got := resolveGenres(meta); got != "" {
		t.Fatalf("manual empty genres = %q", got)
	}
}
