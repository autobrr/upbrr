// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveGenresPreservesUnknownGenres(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
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

func TestResolveOverviewUsesScopedTVOverviewOnlyForEpisodeOrSeasonPack(t *testing.T) {
	t.Parallel()

	answers := map[string]string{}
	ptBR := api.TMDBLocalizedData{
		Overview:        "Series Overview",
		EpisodeOverview: "Episode Overview",
	}

	tests := []struct {
		name string
		meta api.PreparedMetadata
		want string
	}{
		{
			name: "episode upload uses episode overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				SeasonInt:   1,
				EpisodeInt:  2,
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Episode Overview",
		},
		{
			name: "season pack uses season overview from episode field",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				SeasonInt:   1,
				TVPack:      true,
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Episode Overview",
		},
		{
			name: "series upload uses title overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{"pt-BR": ptBR}},
				},
			},
			want: "Series Overview",
		},
		{
			name: "movie ignores episode overview",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
				SeasonInt:   1,
				EpisodeInt:  2,
				ExternalMetadata: api.ExternalMetadata{
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
