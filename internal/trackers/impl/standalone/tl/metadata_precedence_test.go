// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCategoryPrefersManualOriginalLanguage(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "en"}},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalLanguage: "fr", OriginalLanguageProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveCategory(meta); got != "36" {
		t.Fatalf("manual foreign category = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = ""
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	meta.ProviderMetadata.TMDB.OriginalLanguage = "fr"
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual-empty category = %q", got)
	}
}

func TestResolveCategoryAcceptsManualCanonicalEnglish(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}, EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "English", OriginalLanguageProvenance: api.FactProvenanceManual}}
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual English category = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "en"
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual ISO English category = %q", got)
	}
}

func TestResolveCategoryFinalizedDiscAndLanguageFacts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, disc, source, kind, resolution, language, tmdb, genre, want string
		anime                                                             bool
	}{
		{
			name:   "HD DVD disc",
			disc:   "HDDVD",
			source: "HD DVD",
			kind:   "DISC",
			want:   "13",
		},
		{
			name:   "HD DVD remux",
			source: "HDDVD",
			kind:   "REMUX",
			want:   "13",
		},
		{
			name:   "HD DVD encode",
			source: "HDDVD",
			kind:   "ENCODE",
			want:   "14",
		},
		{
			name:   "DVD disc",
			disc:   "DVD",
			source: "DVD",
			kind:   "DISC",
			want:   "12",
		},
		{
			name:   "DVD remux",
			source: "DVD",
			kind:   "REMUX",
			want:   "12",
		},
		{
			name:   "DVD encode",
			source: "DVD",
			kind:   "ENCODE",
			want:   "11",
		},
		{
			name:   "BluRay remux",
			source: "BluRay",
			kind:   "REMUX",
			want:   "13",
		},
		{
			name:   "BluRay encode",
			source: "BluRay",
			kind:   "ENCODE",
			want:   "14",
		},
		{
			name:       "2160p precedence",
			disc:       "HDDVD",
			source:     "HD DVD",
			resolution: "2160p",
			want:       "47",
		},
		{
			name:  "documentary precedence",
			disc:  "HDDVD",
			genre: "Documentary",
			want:  "29",
		},
		{
			name:  "anime precedence",
			disc:  "HDDVD",
			anime: true,
			want:  "34",
		},
		{
			name:     "automatic foreign fallback",
			source:   "HDDVD",
			language: "French",
			want:     "36",
		},
		{
			name:     "TMDB precedence",
			language: "fr",
			tmdb:     "en",
			want:     "32",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				DiscType:          test.disc,
				Source:            test.source,
				Type:              test.kind,
				Anime:             test.anime,
				Release:           api.ReleaseInfo{Resolution: test.resolution, Genre: test.genre},
				EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: test.language},
				ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: test.tmdb}},
			}
			if got := resolveCategory(meta); got != test.want {
				t.Fatalf("category = %q, want %q", got, test.want)
			}
		})
	}
}
