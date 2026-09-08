// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveGenresPreservesUnknownGenres(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Genre: "Sci-Fi,MyCustomGenre",
		},
	}
	answers := map[string]string{}

	got := resolveGenres(meta, answers)
	expected := "Ficção científica, MyCustomGenre"
	if got != expected {
		t.Fatalf("expected genres %q, got %q", expected, got)
	}
}

func TestResolveResolutionUsesResolvedFactOnly(t *testing.T) {
	t.Parallel()

	resolved := resolveResolution(api.UploadSubject{
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		ReleaseName: "Example.Movie.2026.2160p-GRP",
	})
	if resolved["width"] != "1920" || resolved["height"] != "1080" {
		t.Fatalf("resolved dimensions = %#v", resolved)
	}
	rawOnly := resolveResolution(api.UploadSubject{ReleaseName: "Example.Movie.2026.2160p-GRP"})
	if rawOnly["width"] != "" || rawOnly["height"] != "" {
		t.Fatalf("raw-only dimensions = %#v", rawOnly)
	}
}

func TestResolveContainerUsesResolvedFactOnly(t *testing.T) {
	t.Parallel()

	if got := resolveContainer(api.UploadSubject{Container: "mkv", VideoPath: "example.mp4"}); got != "6" {
		t.Fatalf("resolved container = %q", got)
	}
	if got := resolveContainer(api.UploadSubject{VideoPath: "example.mkv", SourcePath: "example.mp4"}); got != "" {
		t.Fatalf("path-only container = %q", got)
	}
	if got := resolveContainer(api.UploadSubject{DiscType: "BDMV"}); got != "5" {
		t.Fatalf("disc container = %q", got)
	}
}

func TestResolveOverviewUsesScopedTVOverviewOnlyForEpisodeOrSeasonPack(t *testing.T) {
	t.Parallel()

	answers := map[string]string{}
	ptBR := api.TMDBLocalizedData{
		Overview:        "Series Overview",
		EpisodeOverview: "Episode Overview",
	}

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "episode upload uses episode overview",
			meta: api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: "TV"},
				SeasonInt:  1,
				EpisodeInt: 2,
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Episode Overview",
		},
		{
			name: "season pack uses season overview from episode field",
			meta: api.UploadSubject{
				Identity:  api.ExternalIdentity{Category: "TV"},
				SeasonInt: 1,
				TVPack:    true,
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Episode Overview",
		},
		{
			name: "series upload uses title overview",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "TV"},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Series Overview",
		},
		{
			name: "movie ignores episode overview",
			meta: api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: "MOVIE"},
				SeasonInt:  1,
				EpisodeInt: 2,
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Series Overview",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveOverview(tc.meta, answers); got != tc.want {
				t.Fatalf("expected overview %q, got %q", tc.want, got)
			}
		})
	}
}
