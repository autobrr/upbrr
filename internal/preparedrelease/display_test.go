// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectPreparedReleaseDisplayCanonicalFixture(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "api", "testdata", "prepared_release_display.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected api.PreparedReleaseDisplay
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}

	release := canonicalDisplayFixtureRelease()
	actual, err := projectPreparedReleaseDisplay(release)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		expectedJSON, err := json.MarshalIndent(expected, "", "  ")
		if err != nil {
			t.Fatalf("marshal expected display: %v", err)
		}
		actualJSON, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			t.Fatalf("marshal actual display: %v", err)
		}
		t.Fatalf("display mismatch\nexpected: %s\nactual: %s", expectedJSON, actualJSON)
	}

	if actual.Providers[0].Details.TMDB == release.ProviderMetadata.TMDB {
		t.Fatal("display details alias prepared release metadata")
	}
	actual.Providers[0].Details.TMDB.OriginCountry[0] = "changed"
	if release.ProviderMetadata.TMDB.OriginCountry[0] != "AU" {
		t.Fatal("display mutation changed prepared release")
	}
	repeated, err := projectPreparedReleaseDisplay(release)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repeated, expected) {
		t.Fatal("repeated projection was not deterministic and detached")
	}
}

func TestProjectPreparedReleaseDisplaySparseAndLocalFallbacks(t *testing.T) {
	t.Parallel()

	identityOnly := api.PreparedRelease{
		Naming:   api.NamingFacts{ReleaseName: "Example.Release.2026-GRP"},
		Identity: api.ExternalIdentity{TMDBID: 101, Category: api.CanonicalCategoryMovie},
	}
	display, err := projectPreparedReleaseDisplay(identityOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(display.Providers) != 0 {
		t.Fatalf("identity-only display providers = %d, want 0", len(display.Providers))
	}

	noSummary := api.PreparedRelease{
		Identity: api.ExternalIdentity{IMDBID: 123456},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{IMDBID: 123456},
		},
	}
	display, err = projectPreparedReleaseDisplay(noSummary)
	if err != nil {
		t.Fatal(err)
	}
	if len(display.Providers) != 1 || display.Providers[0].SummaryAvailable {
		t.Fatalf("no-summary display = %#v", display.Providers)
	}

	fallback := api.PreparedRelease{
		Naming: api.NamingFacts{ReleaseName: "Example.Release.2026-GRP"},
		Identity: api.ExternalIdentity{
			TVmazeID: 404,
			Category: api.CanonicalCategoryTV,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TVmaze: &api.TVmazeMetadata{
				TVmazeID:       404,
				Premiered:      "2026-01-02",
				AverageRuntime: 51,
				Type:           "Scripted",
			},
		},
	}
	display, err = projectPreparedReleaseDisplay(fallback)
	if err != nil {
		t.Fatal(err)
	}
	summary := display.Providers[0].Summary
	if summary.Title != fallback.Naming.ReleaseName || summary.Category != "tv" || summary.Date != "2026-01-02" || summary.RuntimeMinutes != 51 {
		t.Fatalf("fallback summary = %#v", summary)
	}
	if summary.Overview != "" || summary.PosterURL != "" {
		t.Fatalf("fallback crossed provider-local boundary: %#v", summary)
	}
}

func TestProjectPreparedReleaseDisplayRejectsProviderIdentityMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*api.PreparedRelease)
	}{
		{name: "tmdb", mutate: func(value *api.PreparedRelease) { value.ProviderMetadata.TMDB.TMDBID++ }},
		{name: "imdb", mutate: func(value *api.PreparedRelease) { value.ProviderMetadata.IMDB.IMDBID = 0 }},
		{name: "tvdb", mutate: func(value *api.PreparedRelease) { value.ProviderMetadata.TVDB.TVDBID++ }},
		{name: "tvmaze", mutate: func(value *api.PreparedRelease) { value.ProviderMetadata.TVmaze.TVmazeID++ }},
		{name: "mal", mutate: func(value *api.PreparedRelease) { value.ProviderMetadata.AniList.MALID++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := canonicalDisplayFixtureRelease()
			test.mutate(&release)
			if _, err := projectPreparedReleaseDisplay(release); err == nil {
				t.Fatal("expected provider identity mismatch")
			}
		})
	}

	linkedOnly := canonicalDisplayFixtureRelease()
	linkedOnly.ProviderMetadata.TMDB.TMDBID = 0
	linkedOnly.ProviderMetadata.TMDB.IMDBID = linkedOnly.Identity.TMDBID
	if _, err := projectPreparedReleaseDisplay(linkedOnly); err == nil {
		t.Fatal("linked provider ID satisfied missing TMDB own ID")
	}
}

func canonicalDisplayFixtureRelease() api.PreparedRelease {
	return api.PreparedRelease{
		Naming: api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
		Identity: api.ExternalIdentity{
			TMDBID:   101,
			IMDBID:   123456,
			TVDBID:   303,
			TVmazeID: 404,
			MALID:    505,
			Category: api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB:    api.IdentityProvenanceExplicit,
				IMDB:    api.IdentityProvenanceTracker,
				TVDB:    api.IdentityProvenanceScene,
				TVmaze:  api.IdentityProvenanceArr,
				MAL:     api.IdentityProvenanceProvider,
			},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID:           101,
				Category:         "movie",
				Title:            "Example TMDB",
				OriginalTitle:    "Example Original",
				Year:             2026,
				ReleaseDate:      "2026-01-02",
				LastAirDate:      "2026-02-03",
				OriginCountry:    []string{"AU", "NZ"},
				OriginalLanguage: "en",
				Overview:         "TMDB overview",
				Poster:           "https://img.example/tmdb-poster.jpg",
				Backdrop:         "https://img.example/tmdb-backdrop.jpg",
				TMDBType:         "movie",
				Runtime:          123,
				Genres:           "Drama",
				Keywords:         "example, release",
				YouTube:          "https://video.example/tmdb",
			},
			IMDB: &api.IMDBMetadata{
				IMDBID:           123456,
				Title:            "Example IMDb",
				Year:             2025,
				Type:             "movie",
				Plot:             "IMDb overview",
				Rating:           7.4,
				RatingCount:      4567,
				RuntimeMinutes:   119,
				Genres:           "Mystery",
				Country:          "Australia",
				Cover:            "https://img.example/imdb-cover.jpg",
				OriginalLanguage: "en",
			},
			TVDB: &api.TVDBMetadata{
				TVDBID:            303,
				Name:              "Example TVDB",
				Overview:          "TVDB overview",
				FirstAired:        "2024-03-04",
				Year:              2024,
				Type:              "series",
				OriginalCountry:   "FR",
				OriginalLanguage:  "fr",
				Genres:            "Comedy",
				Poster:            "https://img.example/tvdb-poster.jpg",
			},
			TVmaze: &api.TVmazeMetadata{
				TVmazeID:       404,
				Name:           "Example TVmaze",
				Premiered:      "2023-05-06",
				Ended:          "2023-07-08",
				Summary:        "TVmaze overview",
				Type:           "Scripted",
				Language:       "es",
				Genres:         "Thriller",
				AverageRuntime: 47,
				Rating:         8.1,
				Weight:         99,
				Country:        "ES",
				Poster:         "https://img.example/tvmaze-poster.jpg",
				Backdrop:       "https://img.example/tvmaze-backdrop.jpg",
			},
			AniList: &api.AniListMetadata{
				AniListID:       606,
				MALID:           505,
				TitleRomaji:     "Example Anime Romaji",
				TitleEnglish:    "Example Anime",
				Description:     "AniList overview",
				Format:          "TV",
				StartDate:       "2022-09-10",
				EndDate:         "2022-12-11",
				SeasonYear:      2022,
				Duration:        24,
				CountryOfOrigin: "JP",
				CoverExtraLarge: "https://img.example/anilist-cover.jpg",
				BannerImage:     "https://img.example/anilist-banner.jpg",
				Genres:          []string{"Action", "Drama"},
				AverageScore:    82,
				Popularity:      6789,
			},
		},
	}
}
