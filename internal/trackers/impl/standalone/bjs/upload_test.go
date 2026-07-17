// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveRuntimePrefersMediaInfoDuration(t *testing.T) {
	root := t.TempDir()
	miPath := filepath.Join(root, "MEDIAINFO.txt")
	if err := os.WriteFile(miPath, []byte("General\nDuration                                 : 1 h 31 min\n"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	meta := api.UploadSubject{
		MediaInfoTextPath: miPath,
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 91 {
		t.Fatalf("expected MediaInfo runtime 91 minutes, got %d", got)
	}
}

func TestResolveRuntimeFallsBackToExternalMetadata(t *testing.T) {
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 95},
		},
	}

	if got := resolveRuntime(meta); got != 95 {
		t.Fatalf("expected IMDb runtime fallback, got %d", got)
	}
}

func TestResolveRuntimePrefersBDInfoLengthForBDMV(t *testing.T) {
	meta := api.UploadSubject{
		DiscType: "BDMV",
		Disc:     api.DiscFacts{DurationSeconds: 6000},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 100 {
		t.Fatalf("expected BDInfo runtime 100 minutes, got %d", got)
	}
}

func TestParseMediaInfoDurationMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{
			name:     "valid hours and minutes",
			content:  "duration : 2 h 15 m",
			expected: 135,
		},
		{
			name:     "duration string label",
			content:  "Duration/String : 2 h 5 min 2 s",
			expected: 125,
		},
		{
			name:     "duration string1 label",
			content:  "\tDuRaTiOn/String1   : 2 h 5 min 2 s",
			expected: 125,
		},
		{
			name:     "duration spaced slash label",
			content:  "Duration / String2 : 2 hrs 5 mins 2 secs",
			expected: 125,
		},
		{
			name:     "duration string3 colon label",
			content:  "Duration/String3 : 02:05:02.000",
			expected: 125,
		},
		{
			name:     "iso duration value",
			content:  "Duration/String : PT2H5M2S",
			expected: 125,
		},
		{
			name:     "valid milliseconds",
			content:  "duration : 120000",
			expected: 2,
		},
		{
			name:     "empty fields",
			content:  "duration :    ",
			expected: 0,
		},
		{
			name:     "invalid string1 skips to later duration",
			content:  "Duration/String1 : not a duration\nDuration/String2 : 01:30:00.000",
			expected: 90,
		},
		{
			name:     "duration string4 remains ignored",
			content:  "Duration/String4 : 01:30:00.000",
			expected: 0,
		},
		{
			name:     "adjacent duration fields remain ignored",
			content:  "Source_Duration : 01:30:00.000\nDuration_Start : 01:30:00.000",
			expected: 0,
		},
		{
			name:     "no duration keyword",
			content:  "something else",
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMediaInfoDurationMinutes(tc.content)
			if got != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, got)
			}
		})
	}
}

func TestBuildFieldsLimitsDirectorCreatorAndSetsRepack(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Repack: "REPACK",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Creators:      []string{"Creator One", "Creator Two"},
				Directors:     []string{"Director One", "Director Two"},
				OriginCountry: []string{"BR", "US"},
				Title:         "Title",
			},
			IMDB: &api.IMDBMetadata{Year: 2026},
		},
	}

	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["diretor"]; got != "Creator One" {
		t.Fatalf("expected first creator only, got %q", got)
	}
	if got := fields["diretorserie"]; got != "Director One" {
		t.Fatalf("expected first director only, got %q", got)
	}
	if got := fields["repack"]; got != "on" {
		t.Fatalf("expected repack flag, got %q", got)
	}
	if got := fields["pais"]; got != "Brasil, Estados Unidos da América" {
		t.Fatalf("expected translated countries, got %q", got)
	}
}

func TestBuildFieldsWithNilMetadata(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title: "Test TV Title",
		},
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: nil,
			IMDB: nil,
		},
	}

	// Make sure buildFields does not panic and populates fallback title from release
	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["title"]; got != "Test TV Title" {
		t.Fatalf("expected title to fall back to release title, got %q", got)
	}
	if got := fields["pais"]; got != "" {
		t.Fatalf("expected empty country for nil TMDB, got %q", got)
	}
	if got := fields["numtemporadas"]; got != "0" {
		t.Fatalf("expected 0 seasons for nil IMDB, got %q", got)
	}
	if got := fields["idioma"]; got != "Outro" {
		t.Fatalf("expected idioma to fall back to 'Outro' for nil TMDB, got %q", got)
	}
	if got := fields["network"]; got != "" {
		t.Fatalf("expected network to be empty for nil TMDB, got %q", got)
	}
	if got := fields["avaliacao"]; got != "" {
		t.Fatalf("expected avaliacao to be empty for nil IMDB, got %q", got)
	}
	if got := fields["elenco"]; got != "" {
		t.Fatalf("expected elenco to be empty for nil TMDB/IMDB, got %q", got)
	}
	if got := fields["traileryoutube"]; got != "" {
		t.Fatalf("expected traileryoutube to be empty for nil TMDB, got %q", got)
	}
}

func TestBuildFieldsWithTMDBNilAndIMDBPresent(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title: "Test TV Title",
		},
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: nil,
			IMDB: &api.IMDBMetadata{
				Rating: 8.5,
				Stars:  []api.IMDBPerson{{Name: "Star One"}, {Name: "Star Two"}},
			},
		},
	}

	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["avaliacao"]; got != "8.5" {
		t.Fatalf("expected avaliacao from IMDB, got %q", got)
	}
	if got := fields["elenco"]; got != "Star One, Star Two" {
		t.Fatalf("expected elenco from IMDB stars, got %q", got)
	}
	if got := fields["idioma"]; got != "Outro" {
		t.Fatalf("expected idioma to fall back to 'Outro' for nil TMDB, got %q", got)
	}
}

func TestBuildFieldsWithTMDBPresentAndIMDBNil(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title: "Test TV Title",
		},
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				OriginalTitle:    "Original Title",
				Title:            "Title",
				OriginalLanguage: "en",
				Networks:         []api.TMDBNetwork{{Name: "HBO"}},
				Cast:             []string{"Cast One", "Cast Two"},
				YouTube:          "https://youtube.com/watch?v=123",
			},
			IMDB: nil,
		},
	}

	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["title"]; got != "Original Title" {
		t.Fatalf("expected title from TMDB OriginalTitle, got %q", got)
	}
	if got := fields["idioma"]; got != "Inglês" {
		t.Fatalf("expected idioma from TMDB original language, got %q", got)
	}
	if got := fields["network"]; got != "HBO" {
		t.Fatalf("expected network from TMDB, got %q", got)
	}
	if got := fields["elenco"]; got != "Cast One, Cast Two" {
		t.Fatalf("expected elenco from TMDB cast, got %q", got)
	}
	if got := fields["traileryoutube"]; got != "https://youtube.com/watch?v=123" {
		t.Fatalf("expected traileryoutube URL from TMDB, got %q", got)
	}
	if got := fields["avaliacao"]; got != "" {
		t.Fatalf("expected empty avaliacao for nil IMDB, got %q", got)
	}
}

func TestBuildFieldsWithTMDBLocalizedAndIMDBNil(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title: "Test TV Title",
		},
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				OriginalTitle: "Original Title",
				Title:         "Title",
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": {
						Title:      "Título Localizado",
						TrailerURL: "https://youtube.com/watch?v=localized",
					},
				},
			},
			IMDB: nil,
		},
	}

	fields := buildFields(meta, "description", "auth", nil)
	if got := fields["titulobrasileiro"]; got != "Título Localizado" {
		t.Fatalf("expected titulobrasileiro from TMDB localized title, got %q", got)
	}
	if got := fields["traileryoutube"]; got != "https://youtube.com/watch?v=localized" {
		t.Fatalf("expected traileryoutube URL from TMDB localized trailer, got %q", got)
	}
}

func TestResolveOverviewUsesScopedTVOverviewOnlyForEpisodeOrSeasonPack(t *testing.T) {
	t.Parallel()

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
			},
			want: "Episode Overview",
		},
		{
			name: "lowercase episode upload uses episode overview",
			meta: api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: "tv"},
				SeasonInt:  1,
				EpisodeInt: 2,
			},
			want: "Episode Overview",
		},
		{
			name: "season pack uses season overview from episode field",
			meta: api.UploadSubject{
				Identity:  api.ExternalIdentity{Category: "TV"},
				SeasonInt: 1,
				TVPack:    true,
			},
			want: "Episode Overview",
		},
		{
			name: "series upload uses title overview",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "TV"},
			},
			want: "Series Overview",
		},
		{
			name: "movie ignores episode overview",
			meta: api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: "MOVIE"},
				SeasonInt:  1,
				EpisodeInt: 2,
			},
			want: "Series Overview",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveOverview(tc.meta, ptBR); got != tc.want {
				t.Fatalf("expected overview %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBuildDescriptionOmitsBlankEpisodeTitle(t *testing.T) {
	t.Parallel()

	req := trackers.PreparationInput{Meta: api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": {
						EpisodeOverview: "Overview Localizado",
					},
				},
			},
		},
	}}

	got := buildDescription(req, trackers.DescriptionAssets{})
	if strings.Contains(got, "[align=center][/align]") {
		t.Fatalf("expected blank episode title row to be omitted, got %q", got)
	}
	if !strings.Contains(got, "[align=center]Overview Localizado[/align]") {
		t.Fatalf("expected localized episode overview row, got %q", got)
	}
}

func TestBuildDescriptionKeepsEpisodeTitleWhenPresent(t *testing.T) {
	t.Parallel()

	req := trackers.PreparationInput{Meta: api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Localized: map[string]api.TMDBLocalizedData{
					"pt-BR": {
						EpisodeTitle:    "Titulo Localizado",
						EpisodeOverview: "Overview Localizado",
					},
				},
			},
		},
	}}

	got := buildDescription(req, trackers.DescriptionAssets{})
	if !strings.Contains(got, "[align=center]Titulo Localizado[/align]") {
		t.Fatalf("expected localized episode title row, got %q", got)
	}
	if !strings.Contains(got, "[align=center]Overview Localizado[/align]") {
		t.Fatalf("expected localized episode overview row, got %q", got)
	}
}

func TestResolveTagsPreservesUnknownFallbackGenres(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Genre: "Sci-Fi, MyCustomGenre",
		},
	}

	got := resolveTags(meta, api.TMDBLocalizedData{})
	expected := "ficcao.cientifica, mycustomgenre"
	if got != expected {
		t.Fatalf("expected tags %q, got %q", expected, got)
	}
}

func TestResolveTagsFallbackPriority(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{Genre: "ReleaseGenre"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Genres: "Drama"},
			IMDB: &api.IMDBMetadata{Genres: "Comedy"},
		},
	}

	if got := resolveTags(meta, api.TMDBLocalizedData{Genres: "Ação"}); got != "acao" {
		t.Fatalf("expected localized tags to win, got %q", got)
	}
	if got := resolveTags(meta, api.TMDBLocalizedData{}); got != "drama" {
		t.Fatalf("expected TMDB tags to beat IMDb and release, got %q", got)
	}

	meta.ProviderMetadata.TMDB = nil
	if got := resolveTags(meta, api.TMDBLocalizedData{}); got != "comedia" {
		t.Fatalf("expected IMDb tags to beat release, got %q", got)
	}
}

func TestBuildFieldsTagsAnswerOverridesComputedTags(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{Genre: "Drama"},
	}

	fields := buildFields(meta, "description", "auth", map[string]string{"tags": "manual.tag"})
	if got := fields["tags"]; got != "manual.tag" {
		t.Fatalf("expected manual tags answer, got %q", got)
	}
}

func TestResolveAdultLocalizedAndCanonicalGenres(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "localized Portuguese adult genre",
			meta: api.UploadSubject{
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{
						"pt-BR": {Genres: "Adulto"},
					}},
				},
			},
			want: "1",
		},
		{
			name: "IMDb-only adult genre",
			meta: api.UploadSubject{
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Genres: "Adult"},
				},
			},
			want: "1",
		},
		{
			name: "anime hentai detection remains adult",
			meta: api.UploadSubject{
				Anime:   true,
				Release: api.ReleaseInfo{Genre: "Hentai"},
			},
			want: "1",
		},
		{
			name: "nonadult Portuguese genre stays nonadult",
			meta: api.UploadSubject{
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Localized: map[string]api.TMDBLocalizedData{
						"pt-BR": {Genres: "Drama"},
					}},
				},
			},
			want: "2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveAdult(tc.meta); got != tc.want {
				t.Fatalf("expected adulto=%q, got %q", tc.want, got)
			}
		})
	}
}
