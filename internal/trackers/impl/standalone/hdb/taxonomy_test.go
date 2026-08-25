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

	const sourcePath = "Example.Release.2026.mkv"

	tests := []struct {
		name     string
		identity api.ExternalIdentity
		metadata api.SourceScopedMetadata
		want     int
	}{
		{
			name: "movie documentary from IMDb",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				IMDBID:     1234567,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Genres: "Drama, Documentary"},
			},
			want: 3,
		},
		{
			name: "TV documentary from TVDB",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryTV,
				TVDBID:     765432,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				TVDB:       &api.TVDBMetadata{TVDBID: 765432, Genres: "Documentary, History"},
			},
			want: 3,
		},
		{
			name: "movie ignores TMDB documentary",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				IMDBID:     1234567,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				TMDB:       &api.TMDBMetadata{Genres: "Documentary", GenreIDs: "99"},
				IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Genres: "Drama"},
			},
			want: 1,
		},
		{
			name: "TV ignores IMDb documentary",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryTV,
				IMDBID:     1234567,
				TVDBID:     765432,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Genres: "Documentary"},
				TVDB:       &api.TVDBMetadata{TVDBID: 765432, Genres: "Drama"},
			},
			want: 2,
		},
		{
			name: "movie ignores mismatched IMDb documentary",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				IMDBID:     1234567,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				IMDB:       &api.IMDBMetadata{IMDBID: 7654321, Genres: "Documentary"},
			},
			want: 1,
		},
		{
			name: "TV ignores stale TVDB documentary",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Generation: 2,
				Category:   api.CanonicalCategoryTV,
				TVDBID:     765432,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				Generation: 1,
				TVDB:       &api.TVDBMetadata{TVDBID: 765432, Genres: "Documentary"},
			},
			want: 2,
		},
		{
			name: "concert",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				IMDBID:     1234567,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				IMDB: &api.IMDBMetadata{
					IMDBID: 1234567,
					Type:   "Video",
					Genres: "Music",
				},
			},
			want: 4,
		},
		{
			name: "movie documentary takes precedence over concert",
			identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Category:   api.CanonicalCategoryMovie,
				IMDBID:     1234567,
			},
			metadata: api.SourceScopedMetadata{
				SourcePath: sourcePath,
				IMDB: &api.IMDBMetadata{
					IMDBID: 1234567,
					Type:   "Video",
					Genres: "Documentary, Music",
				},
			},
			want: 3,
		},
		{
			name:     "movie",
			identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			want:     1,
		},
		{
			name:     "TV",
			identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
			want:     2,
		},
		{
			name:     "unknown",
			identity: api.ExternalIdentity{Category: api.CanonicalCategoryUnknown},
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hdbCategoryID(api.UploadSubject{
				SourcePath:       sourcePath,
				Identity:         tt.identity,
				ProviderMetadata: tt.metadata,
			}); got != tt.want {
				t.Fatalf("upload category ID = %d, want %d", got, tt.want)
			}
			if got := hdbDupeCategoryID(api.DuplicateSubject{
				SourcePath:       sourcePath,
				Identity:         tt.identity,
				ProviderMetadata: tt.metadata,
			}); got != tt.want {
				t.Fatalf("duplicate-search category ID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildUploadFieldsUsesDocumentaryCategory(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		SourcePath: "Example.Release.2026.mkv",
		Identity: api.ExternalIdentity{
			SourcePath: "Example.Release.2026.mkv",
			Category:   api.CanonicalCategoryMovie,
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "Example.Release.2026.mkv",
			IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Genres: "Documentary"},
		},
	}
	fields := buildUploadFields(meta, config.Config{}, hdbCategoryID(meta), 5, 6, "description", "Example.Release.2026.1080p-GRP")

	if got := fields["category"]; got != "3" {
		t.Fatalf("upload category field = %q, want %q", got, "3")
	}
}
