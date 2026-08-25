// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBCategoryIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category api.CanonicalCategory
		tmdb     *api.TMDBMetadata
		imdb     *api.IMDBMetadata
		want     int
	}{
		{
			name:     "movie documentary genre",
			category: api.CanonicalCategoryMovie,
			tmdb:     &api.TMDBMetadata{Genres: "Documentary"},
			want:     3,
		},
		{
			name:     "TV documentary keyword",
			category: api.CanonicalCategoryTV,
			tmdb:     &api.TMDBMetadata{Keywords: "documentary filmmaking"},
			want:     3,
		},
		{
			name:     "localized documentary genre ID",
			category: api.CanonicalCategoryMovie,
			tmdb:     &api.TMDBMetadata{Genres: "Documentario", GenreIDs: "18, 99"},
			want:     3,
		},
		{
			name:     "concert",
			category: api.CanonicalCategoryMovie,
			imdb:     &api.IMDBMetadata{Type: "Concert"},
			want:     4,
		},
		{
			name:     "movie",
			category: api.CanonicalCategoryMovie,
			want:     1,
		},
		{
			name:     "TV",
			category: api.CanonicalCategoryTV,
			want:     2,
		},
		{
			name:     "unknown",
			category: api.CanonicalCategoryUnknown,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := api.ExternalIdentity{Category: tt.category}
			metadata := api.SourceScopedMetadata{TMDB: tt.tmdb, IMDB: tt.imdb}

			if got := hdbCategoryID(api.UploadSubject{Identity: identity, ProviderMetadata: metadata}); got != tt.want {
				t.Fatalf("upload category ID = %d, want %d", got, tt.want)
			}
			if got := hdbDupeCategoryID(api.DuplicateSubject{Identity: identity, ProviderMetadata: metadata}); got != tt.want {
				t.Fatalf("duplicate-search category ID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildUploadFieldsUsesDocumentaryCategory(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{GenreIDs: "99"},
		},
	}
	fields := buildUploadFields(meta, config.Config{}, hdbCategoryID(meta), 5, 6, "description", "Example.Release.2026.1080p-GRP")

	if got := fields["category"]; got != "3" {
		t.Fatalf("upload category field = %q, want %q", got, "3")
	}
}
