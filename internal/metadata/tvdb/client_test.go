// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectBestSeries(t *testing.T) {
	results := []SeriesSearchResult{
		{TVDBID: 1, Name: "Show A", Year: "2020"},
		{TVDBID: 2, Name: "Show B", Year: "2021", Aliases: []Alias{{Name: "Show B (2022)", Language: "eng"}}},
		{TVDBID: 3, Name: "Show C", Year: "2023", Aliases: []Alias{{Name: "Show C 2024", Language: "eng"}}},
	}
	if id := selectBestSeries(results, "2021"); id != 2 {
		t.Fatalf("expected 2, got %d", id)
	}
	if id := selectBestSeries(results, "2022"); id != 2 {
		t.Fatalf("expected 2 via alias, got %d", id)
	}
	if id := selectBestSeries(results, "2024"); id != 3 {
		t.Fatalf("expected 3 via plain alias year, got %d", id)
	}
	if id := selectBestSeries(results, ""); id != 1 {
		t.Fatalf("expected 1 default, got %d", id)
	}
}

func TestSpecificSeriesAliasDoesNotUseSlugFallback(t *testing.T) {
	data := EpisodesData{
		Aliases: []Alias{
			{Name: "Titre FR", Language: "fra"},
			{Name: "Cats Eye", Language: "eng"},
		},
		Slug: "cats-eye-2025",
	}
	if got := specificSeriesAlias(data); got != "" {
		t.Fatalf("expected no slug fallback alias, got %q", got)
	}

	aliasesWithYear := []Alias{
		{Name: "Cats Eye", Language: "eng"},
		{Name: "Cats Eye (2024)", Language: "eng"},
	}
	if got := specificSeriesAlias(EpisodesData{Aliases: aliasesWithYear, Slug: "cats-eye-2025"}); got != "Cats Eye (2024)" {
		t.Fatalf("expected explicit alias year to win, got %q", got)
	}

	if got := specificSeriesAlias(EpisodesData{SeriesTitle: "Cats Eye", SeriesYear: 2025}); got != "Cats Eye" {
		t.Fatalf("expected source-less series year to be ignored, got %q", got)
	}

	if got := specificSeriesAlias(EpisodesData{SeriesTitle: "Cats Eye", SeriesYear: 2025, SeriesYearSource: seriesYearSourceTranslationAlias}); got != "Cats Eye (2025)" {
		t.Fatalf("expected explicit series title/year, got %q", got)
	}

	if got := specificSeriesAlias(EpisodesData{SeriesTitle: "Hunter x Hunter (2011)", SeriesYear: 2011, SeriesYearSource: seriesYearSourceTranslationName}); got != "Hunter x Hunter (2011)" {
		t.Fatalf("expected title year not to be duplicated, got %q", got)
	}
}

func TestFindEpisodeMatch(t *testing.T) {
	episodes := []Episode{
		{ID: 10, SeasonNumber: 1, Number: 1, AbsoluteNumber: 1, Name: "Pilot", Overview: "Overview", SeasonName: "Season 1", Year: 2020, Aired: "2020-01-01"},
		{ID: 11, SeasonNumber: 1, Number: 2, AbsoluteNumber: 2, Name: "Second", Overview: "Overview 2", SeasonName: "Season 1", Year: 2020, Aired: "2020-01-08"},
	}

	match, ok := findEpisodeMatch(episodes, EpisodeQuery{AiredDate: "2020-01-08"})
	if !ok || match.EpisodeID != 11 {
		t.Fatalf("expected match by airdate")
	}

	match, ok = findEpisodeMatch(episodes, EpisodeQuery{Season: 1, Episode: 0})
	if !ok || match.EpisodeID != 10 {
		t.Fatalf("expected season first episode")
	}

	match, ok = findEpisodeMatch(episodes, EpisodeQuery{Season: 1, Episode: 2})
	if !ok || match.EpisodeID != 11 {
		t.Fatalf("expected exact episode match")
	}

	match, ok = findEpisodeMatch(episodes, EpisodeQuery{Season: 1, Episode: 2})
	if !ok || match.EpisodeNumber != 2 {
		t.Fatalf("expected episode number 2")
	}
	if match.Aired != "2020-01-08" {
		t.Fatalf("expected aired date to propagate, got %q", match.Aired)
	}

	match, ok = findEpisodeMatch(episodes, EpisodeQuery{Season: 1, Episode: 3, Absolute: 2})
	if !ok || match.EpisodeID != 11 {
		t.Fatalf("expected absolute number match")
	}
}

func TestEpisodesResponseUnmarshal(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantEpisodes   int
		wantTopSlug    string
		wantDataSlug   string
		wantFirstYear  int
		checkFirstYear bool
		wantErrSubstr  string
	}{
		{
			name:           "array_data",
			body:           `{"data":[{"id":101,"seasonNumber":1,"number":2,"year":2025}],"slug":"show-2025"}`,
			wantEpisodes:   1,
			wantTopSlug:    "show-2025",
			wantFirstYear:  2025,
			checkFirstYear: true,
		},
		{
			name:           "object_data_with_episodes",
			body:           `{"data":{"episodes":[{"id":201,"seasonNumber":2,"number":3,"year":"2026"}],"slug":"inner-2026"}}`,
			wantEpisodes:   1,
			wantDataSlug:   "inner-2026",
			wantFirstYear:  2026,
			checkFirstYear: true,
		},
		{
			name:         "null_data",
			body:         `{"data":null,"slug":"show"}`,
			wantEpisodes: 0,
			wantTopSlug:  "show",
		},
		{
			name:          "invalid_scalar_data",
			body:          `{"data":1}`,
			wantErrSubstr: "unsupported JSON type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp episodesResponse
			err := json.Unmarshal([]byte(tc.body), &resp)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if got := len(resp.Data.Episodes); got != tc.wantEpisodes {
				t.Fatalf("expected %d episodes, got %d", tc.wantEpisodes, got)
			}
			if resp.Slug != tc.wantTopSlug {
				t.Fatalf("expected top-level slug %q, got %q", tc.wantTopSlug, resp.Slug)
			}
			if resp.Data.Slug != tc.wantDataSlug {
				t.Fatalf("expected data slug %q, got %q", tc.wantDataSlug, resp.Data.Slug)
			}
			if tc.checkFirstYear {
				if got := int(resp.Data.Episodes[0].Year); got != tc.wantFirstYear {
					t.Fatalf("expected first episode year %d, got %d", tc.wantFirstYear, got)
				}
			}
		})
	}
}

func TestSearchSeriesAlwaysUsesEnglishLanguage(t *testing.T) {
	searchLang := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/search":
			searchLang = r.URL.Query().Get("lang")
			_, _ = w.Write([]byte(`{"data":[{"tvdb_id":1,"name":"Example","year":"2020","aliases":[]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	_, _, err := client.SearchSeries(context.Background(), "Example", "2020")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if searchLang != "eng" {
		t.Fatalf("expected search lang %q, got %q", "eng", searchLang)
	}
}

func TestSearchSeriesDecodesStringAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/search":
			_, _ = w.Write([]byte(`{"data":[{"tvdb_id":252322,"name":"Hunter x Hunter","year":"2011","aliases":["Hunter x Hunter (2011)"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	results, selected, err := client.SearchSeries(context.Background(), "Hunter x Hunter", "2011")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if selected != 252322 {
		t.Fatalf("expected selected id 252322, got %d", selected)
	}
	if len(results) != 1 || len(results[0].Aliases) != 1 {
		t.Fatalf("expected decoded alias, got %#v", results)
	}
	if results[0].Aliases[0].Name != "Hunter x Hunter (2011)" || results[0].Aliases[0].Language != "eng" {
		t.Fatalf("expected english string alias, got %#v", results[0].Aliases[0])
	}
}

func TestGetEpisodesLanguagePreference(t *testing.T) {
	episodeLang := ""
	extendedLang := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			episodeLang = r.URL.Query().Get("lang")
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"example-2025"}}`))
		case "/series/12/extended":
			extendedLang = r.URL.Query().Get("lang")
			_, _ = w.Write([]byte(`{"data":{"aliases":[{"name":"Example","language":"eng"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	_, _, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if episodeLang != "eng" {
		t.Fatalf("expected episodes request lang=eng, got %q", episodeLang)
	}
	if extendedLang != "eng" {
		t.Fatalf("expected extended request lang=eng, got %q", extendedLang)
	}
}

func TestGetEpisodesExtractsScheduleHints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot","aired":"2020-01-01"}],"slug":"example-2025"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"aliases":[{"name":"Example","language":"eng"}],"airsDays":{"monday":true,"wednesday":true},"airsTime":"20:00","airsTimeZone":"Australia/Sydney"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, _, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if data.AirsTime != "20:00" {
		t.Fatalf("expected airs time, got %q", data.AirsTime)
	}
	if data.AirsTimezone != "Australia/Sydney" {
		t.Fatalf("expected airs timezone, got %q", data.AirsTimezone)
	}
	if len(data.AirsDays) != 2 || data.AirsDays[0] != "Monday" || data.AirsDays[1] != "Wednesday" {
		t.Fatalf("expected parsed airs days, got %#v", data.AirsDays)
	}
}

func TestGetEpisodesUsesTranslationSeriesMetadataWithoutSlugFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"cats-eye-2025"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"キャッツ・アイ","slug":"cats-eye-2025","aliases":[{"name":"Cats Eye","language":"eng"}]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"Cat's Eye","aliases":["Cats Eye"]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "Cat's Eye" {
		t.Fatalf("expected translated title without slug year, got %q", alias)
	}
	if data.SeriesYear != 0 {
		t.Fatalf("expected no series year from slug while english aliases exist, got %d", data.SeriesYear)
	}
}

func TestGetEpisodesIgnoresAPIYearForNamingYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"a-spy-among-friends"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"A Spy Among Friends","slug":"a-spy-among-friends","year":2022,"firstAired":"2022-12-08","aliases":[]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"A Spy Among Friends","aliases":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "A Spy Among Friends" {
		t.Fatalf("expected translated title without api year, got %q", alias)
	}
	if data.SeriesYear != 0 || data.SeriesYearSource != "" {
		t.Fatalf("expected no naming year from api year, got year=%d source=%q", data.SeriesYear, data.SeriesYearSource)
	}
}

func TestGetEpisodesUsesExplicitTranslationAliasYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"cats-eye-2025"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"キャッツ・アイ","slug":"cats-eye-2025","aliases":[{"name":"Cats Eye","language":"eng"}]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"Cat's Eye","aliases":["Cat's Eye (2025)"]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "Cat's Eye (2025)" {
		t.Fatalf("expected explicit translation alias year, got %q", alias)
	}
	if data.SeriesYear != 2025 {
		t.Fatalf("expected explicit series year 2025, got %d", data.SeriesYear)
	}
	if data.SeriesYearSource != seriesYearSourceTranslationAlias || data.SeriesYearConfidence != seriesYearConfidenceHigh {
		t.Fatalf("expected translation alias source/high confidence, got source=%q confidence=%q", data.SeriesYearSource, data.SeriesYearConfidence)
	}
}

func TestGetEpisodesUpgradesLegacyCachedAliasYearSource(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "12-eng.json")
	if err := writeEpisodesCache(cachePath, EpisodesData{
		Episodes: []Episode{
			{ID: 1, SeasonNumber: 1, Number: 1, Name: "Pilot"},
		},
		Aliases: []Alias{
			{Name: "Cat's Eye", Language: "eng"},
			{Name: "Cat's Eye (2025)", Language: "eng"},
		},
		SeriesTitle: "Cat's Eye",
		SeriesYear:  2025,
	}); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", cacheDir)
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "Cat's Eye (2025)" {
		t.Fatalf("expected legacy cached alias year to remain naming-eligible, got %q", alias)
	}
	if data.SeriesYearSource != seriesYearSourceExtendedAlias || data.SeriesYearConfidence != seriesYearConfidenceHigh {
		t.Fatalf("expected upgraded legacy source/high confidence, got source=%q confidence=%q", data.SeriesYearSource, data.SeriesYearConfidence)
	}

	refreshed, ok := readEpisodesCache(cachePath)
	if !ok {
		t.Fatalf("expected upgraded cache to be readable")
	}
	if refreshed.SeriesYearSource != seriesYearSourceExtendedAlias || refreshed.SeriesYearConfidence != seriesYearConfidenceHigh {
		t.Fatalf("expected upgraded cache provenance, got source=%q confidence=%q", refreshed.SeriesYearSource, refreshed.SeriesYearConfidence)
	}
}

func TestGetEpisodesDoesNotUpgradeUnprovenLegacyCachedYear(t *testing.T) {
	cacheDir := t.TempDir()
	if err := writeEpisodesCache(filepath.Join(cacheDir, "12-eng.json"), EpisodesData{
		Episodes: []Episode{
			{ID: 1, SeasonNumber: 1, Number: 1, Name: "Pilot"},
		},
		SeriesTitle: "A Spy Among Friends",
		SeriesYear:  2022,
	}); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", cacheDir)
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "A Spy Among Friends" {
		t.Fatalf("expected unproven cached year to stay out of alias, got %q", alias)
	}
	if data.SeriesYearSource != "" || data.SeriesYearConfidence != "" {
		t.Fatalf("expected no legacy provenance without title/alias proof, got source=%q confidence=%q", data.SeriesYearSource, data.SeriesYearConfidence)
	}
}

func TestSeriesTranslationMetadataUsesSelectedFallbackAliasYear(t *testing.T) {
	t.Run("selected alias year", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Aliases: []string{"Same Show (2020)", "Same Show (2024)"},
		})

		if got.title != "Same Show (2024)" {
			t.Fatalf("expected selected fallback alias title, got %q", got.title)
		}
		if got.year != 2024 {
			t.Fatalf("expected selected fallback alias year 2024, got %d", got.year)
		}
		if got.yearSource != seriesYearSourceTranslationAlias || got.yearConfidence != seriesYearConfidenceHigh {
			t.Fatalf("expected translation alias source/high confidence, got source=%q confidence=%q", got.yearSource, got.yearConfidence)
		}
	})

	t.Run("selected alias without year", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Aliases: []string{"Same Show (2020)", "Same Show"},
		})

		if got.title != "Same Show" {
			t.Fatalf("expected selected fallback alias title, got %q", got.title)
		}
		if got.year != 0 || got.yearSource != "" || got.yearConfidence != "" {
			t.Fatalf("expected no year from unselected matching alias, got year=%d source=%q confidence=%q", got.year, got.yearSource, got.yearConfidence)
		}
	})

	t.Run("clean selected alias uses extended alias year", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{
			Aliases: []aliasResponse{
				{Name: "Cats Eye (2025)", Language: "eng"},
			},
		}, seriesTranslationDataResponse{
			Aliases: []string{"Cats Eye"},
		})

		if got.title != "Cats Eye" {
			t.Fatalf("expected selected fallback alias title, got %q", got.title)
		}
		if got.year != 2025 {
			t.Fatalf("expected extended alias year 2025, got %d", got.year)
		}
		if got.yearSource != seriesYearSourceExtendedAlias || got.yearConfidence != seriesYearConfidenceHigh {
			t.Fatalf("expected extended alias source/high confidence, got source=%q confidence=%q", got.yearSource, got.yearConfidence)
		}
	})

	t.Run("alias suffix year beats title year", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Name:    "Hunter x Hunter (2011)",
			Aliases: []string{"Hunter x Hunter (2011) (2024)"},
		})

		if got.title != "Hunter x Hunter (2011)" {
			t.Fatalf("expected translation name title, got %q", got.title)
		}
		if got.year != 2011 || got.yearSource != seriesYearSourceTranslationName {
			t.Fatalf("expected translation name year to win for explicit title, got year=%d source=%q", got.year, got.yearSource)
		}

		got = seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Name:    "Hunter x Hunter 2011",
			Aliases: []string{"Hunter x Hunter 2011 (2024)"},
		})

		if got.year != 2024 {
			t.Fatalf("expected explicit alias suffix year 2024, got %d", got.year)
		}
		if got.yearSource != seriesYearSourceTranslationAlias || got.yearConfidence != seriesYearConfidenceHigh {
			t.Fatalf("expected translation alias source/high confidence, got source=%q confidence=%q", got.yearSource, got.yearConfidence)
		}
	})
}

func TestSeriesTranslationMetadataUsesOnlyExplicitTranslationNameYear(t *testing.T) {
	t.Run("parenthesized year", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Name: "Hunter x Hunter (2011)",
		})

		if got.year != 2011 || got.yearSource != seriesYearSourceTranslationName {
			t.Fatalf("expected explicit translation name year, got year=%d source=%q", got.year, got.yearSource)
		}
	})

	t.Run("bare numeric title", func(t *testing.T) {
		got := seriesTranslationMetadata(seriesExtendedDataResponse{}, seriesTranslationDataResponse{
			Name: "Show 2024",
		})

		if got.title != "Show 2024" {
			t.Fatalf("expected title preserved, got %q", got.title)
		}
		if got.year != 0 || got.yearSource != "" || got.yearConfidence != "" {
			t.Fatalf("expected bare numeric title not to become naming year, got year=%d source=%q confidence=%q", got.year, got.yearSource, got.yearConfidence)
		}
	})
}

func TestGetEpisodesUsesTranslationNameYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"hunter-x-hunter-2011"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"ハンター×ハンター","slug":"hunter-x-hunter-2011","aliases":[]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"Hunter x Hunter (2011)","aliases":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, alias, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if alias != "Hunter x Hunter (2011)" {
		t.Fatalf("expected translation name year without duplication, got %q", alias)
	}
	if data.SeriesYear != 2011 || data.SeriesYearSource != seriesYearSourceTranslationName {
		t.Fatalf("expected translation name year, got year=%d source=%q", data.SeriesYear, data.SeriesYearSource)
	}
}

func TestGetEpisodesUsesSlugFallbackWhenNoEnglishEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"hunter-x-hunter-2011"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"ハンター×ハンター","slug":"hunter-x-hunter-2011","aliases":[]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"","aliases":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, _, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if data.SeriesYear != 2011 || data.SeriesYearSource != seriesYearSourceSlug || data.SeriesYearConfidence != seriesYearConfidenceLow {
		t.Fatalf("expected low-confidence slug year, got year=%d source=%q confidence=%q", data.SeriesYear, data.SeriesYearSource, data.SeriesYearConfidence)
	}
}

func TestGetEpisodesRejectsMismatchedSlugFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/12/episodes/default":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"id":1,"seasonNumber":1,"number":1,"name":"Pilot"}],"slug":"wrong-show-2025"}}`))
		case "/series/12/extended":
			_, _ = w.Write([]byte(`{"data":{"name":"Original Show","slug":"wrong-show-2025","aliases":[]}}`))
		case "/series/12/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"","aliases":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	data, _, err := client.GetEpisodes(context.Background(), 12, EpisodeQuery{Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("get episodes failed: %v", err)
	}
	if data.SeriesYear != 0 || data.SeriesYearSource != "" {
		t.Fatalf("expected mismatched slug year rejected, got year=%d source=%q", data.SeriesYear, data.SeriesYearSource)
	}
}

func TestGetSeriesMetadataWithLanguageDerivesEnglishFromAlias(t *testing.T) {
	requestedLang := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/411800/extended":
			requestedLang = r.URL.Query().Get("lang")
			_, _ = w.Write([]byte(`{"data":{"id":411800,"name":"アークナイツ","overview":"日本語概要","originalLanguage":"jpn","nameTranslations":["jpn","eng"],"overviewTranslations":["jpn","eng"],"aliases":[{"language":"eng","name":"Arknights [PRELUDE TO DAWN]"},{"language":"eng","name":"Arknights: Prelude to Dawn"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	metadata, err := client.GetSeriesMetadataWithLanguage(context.Background(), 411800, "")
	if err != nil {
		t.Fatalf("get series metadata failed: %v", err)
	}
	if requestedLang != "" {
		t.Fatalf("expected lang none request, got %q", requestedLang)
	}
	if metadata.NameEnglish != "Arknights: Prelude to Dawn" {
		t.Fatalf("expected clean english alias selection, got %q", metadata.NameEnglish)
	}
	if metadata.OverviewEnglish != "" {
		t.Fatalf("expected empty english overview when not present, got %q", metadata.OverviewEnglish)
	}
	if !metadata.HasEnglish {
		t.Fatalf("expected HasEnglish true when english name is populated")
	}
}

func TestGetSeriesMetadataWithLanguageUsesTranslationEndpointForEnglishText(t *testing.T) {
	translationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/411800/extended":
			_, _ = w.Write([]byte(`{"data":{"id":411800,"name":"アークナイツ","overview":"日本語概要","originalLanguage":"jpn","nameTranslations":["jpn","eng"],"overviewTranslations":["jpn","eng"],"aliases":[{"language":"eng","name":"Arknights: Prelude to Dawn"}]}}`))
		case "/series/411800/translations/eng":
			translationCalls++
			_, _ = w.Write([]byte(`{"data":{"name":"Arknights: Prelude to Dawn","overview":"In the world of Terra..."}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	metadata, err := client.GetSeriesMetadataWithLanguage(context.Background(), 411800, "")
	if err != nil {
		t.Fatalf("get series metadata failed: %v", err)
	}
	if translationCalls != 1 {
		t.Fatalf("expected one english translation request, got %d", translationCalls)
	}
	if metadata.NameEnglish != "Arknights: Prelude to Dawn" {
		t.Fatalf("expected english name from translation/alias, got %q", metadata.NameEnglish)
	}
	if metadata.OverviewEnglish != "In the world of Terra..." {
		t.Fatalf("expected english overview from translation endpoint, got %q", metadata.OverviewEnglish)
	}
	if !metadata.HasEnglish {
		t.Fatalf("expected HasEnglish true when english text is populated")
	}
}

func TestGetSeriesMetadataWithLanguageNoResponseDump(t *testing.T) {
	dumpDir := filepath.Join(t.TempDir(), "tvdb_api_responses")
	t.Setenv("UA_TVDB_RESPONSE_DUMP_DIR", dumpDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/series/1/extended":
			_, _ = w.Write([]byte(`{"data":{"id":1,"name":"Example","overview":"Example Overview","originalLanguage":"eng"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	if _, err := client.GetSeriesMetadataWithLanguage(context.Background(), 1, ""); err != nil {
		t.Fatalf("get series metadata failed: %v", err)
	}

	entries, err := os.ReadDir(dumpDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no response dump files, got %d", len(entries))
	}
}

func TestGetEpisodeTranslation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/episodes/101/translations/eng":
			_, _ = w.Write([]byte(`{"data":{"name":"Episode 2","overview":"English episode overview"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key", "")
	client.baseURL = server.URL

	translated, err := client.GetEpisodeTranslation(context.Background(), 101, "eng")
	if err != nil {
		t.Fatalf("get episode translation failed: %v", err)
	}
	if translated.Name != "Episode 2" {
		t.Fatalf("expected translated name, got %q", translated.Name)
	}
	if translated.Overview != "English episode overview" {
		t.Fatalf("expected translated overview, got %q", translated.Overview)
	}
}
