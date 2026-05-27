// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubArrClient struct {
	result ArrLookupResult
	err    error
	calls  int
	last   api.PreparedMetadata
}

func (s *stubArrClient) Lookup(_ context.Context, meta api.PreparedMetadata) (ArrLookupResult, error) {
	s.calls++
	s.last = meta
	if s.err != nil {
		return ArrLookupResult{}, s.err
	}
	return s.result, nil
}

func TestApplyArrDataUsesSonarrForTV(t *testing.T) {
	repo := &fakeRepo{}
	sonarr := &stubArrClient{
		result: ArrLookupResult{
			Source:       "sonarr",
			TMDBID:       101,
			IMDBID:       202,
			TVDBID:       303,
			TVmazeID:     404,
			Year:         2020,
			Genres:       []string{"Drama", "Anime"},
			ReleaseGroup: "GROUP",
		},
	}
	radarr := &stubArrClient{}
	svc := NewService(
		repo,
		WithConfig(config.Config{
			ArrIntegration: config.ArrIntegrationConfig{
				UseSonarr: true,
				UseRadarr: true,
			},
		}),
		WithSonarrClient(sonarr),
		WithRadarrClient(radarr),
	)

	meta, err := svc.ApplyArrData(context.Background(), api.PreparedMetadata{
		SourcePath: "/data/Show.S01E01.mkv",
		SeasonInt:  1,
		EpisodeInt: 1,
		Release: api.ReleaseInfo{
			Title: "Show",
		},
	})
	if err != nil {
		t.Fatalf("ApplyArrData returned error: %v", err)
	}
	if sonarr.calls != 1 {
		t.Fatalf("expected sonarr lookup, got %d calls", sonarr.calls)
	}
	if radarr.calls != 0 {
		t.Fatalf("expected radarr not called, got %d calls", radarr.calls)
	}
	if meta.ArrSource != "sonarr" || meta.ArrTMDBID != 101 || meta.ArrIMDBID != 202 || meta.ArrTVDBID != 303 || meta.ArrTVmazeID != 404 {
		t.Fatalf("unexpected arr ids: %#v", meta)
	}
	if meta.ArrYear != 2020 {
		t.Fatalf("expected arr year 2020, got %d", meta.ArrYear)
	}
	if !meta.Anime {
		t.Fatalf("expected anime hint from arr genres")
	}
	if meta.ArrReleaseGroup != "GROUP" {
		t.Fatalf("expected release group GROUP, got %q", meta.ArrReleaseGroup)
	}
}

func TestResolveExternalIDsPrefersArrBeforeSearch(t *testing.T) {
	repo := &fakeRepo{}
	tmdbClient := &stubTMDB{
		searchOutcome: tmdb.SearchOutcome{TMDBID: 999, Category: "MOVIE"},
		metadata:      tmdb.MetadataResult{Title: "Example", Year: 2021},
	}
	imdbClient := &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 888},
		info:         imdb.Info{IMDbID: "tt0000123", Title: "Example", Year: 2021},
	}
	svc := NewService(
		repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "token"}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/data/Example.2021.mkv",
		ArrSource:  "radarr",
		ArrTMDBID:  123,
		ArrIMDBID:  456,
	})
	if err != nil {
		t.Fatalf("ResolveExternalIDs returned error: %v", err)
	}
	if result.ExternalIDs.TMDBID != 123 || result.ExternalIDs.SourceTMDB != "radarr" {
		t.Fatalf("expected arr tmdb id preserved, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.IMDBID != 456 || result.ExternalIDs.SourceIMDB != "radarr" {
		t.Fatalf("expected arr imdb id preserved, got %#v", result.ExternalIDs)
	}
	if tmdbClient.searchCalls != 0 {
		t.Fatalf("expected tmdb search skipped, got %d calls", tmdbClient.searchCalls)
	}
	if imdbClient.searchCalls != 0 {
		t.Fatalf("expected imdb search skipped, got %d calls", imdbClient.searchCalls)
	}
}

func TestResolveExternalIDsDoesNotOverwriteExplicitOverridesWithArr(t *testing.T) {
	repo := &fakeRepo{}
	overrideTMDB := 555
	overrideIMDB := 777
	svc := NewService(
		repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "token"}}),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2022}}),
		WithIMDBClient(&stubIMDB{info: imdb.Info{IMDbID: "tt0000777", Title: "Example", Year: 2022}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.ResolveExternalIDs(context.Background(), api.PreparedMetadata{
		SourcePath: "/data/Example.2022.mkv",
		ArrSource:  "radarr",
		ArrTMDBID:  123,
		ArrIMDBID:  456,
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID: &overrideTMDB,
			IMDBID: &overrideIMDB,
		},
	})
	if err != nil {
		t.Fatalf("ResolveExternalIDs returned error: %v", err)
	}
	if result.ExternalIDs.TMDBID != overrideTMDB || result.ExternalIDs.SourceTMDB != "override" {
		t.Fatalf("expected tmdb override retained, got %#v", result.ExternalIDs)
	}
	if result.ExternalIDs.IMDBID != overrideIMDB || result.ExternalIDs.SourceIMDB != "override" {
		t.Fatalf("expected imdb override retained, got %#v", result.ExternalIDs)
	}
}

func TestResolveSearchYearUsesArrYearBeforeReleaseYear(t *testing.T) {
	year := resolveSearchYear(api.PreparedMetadata{
		ArrYear: 2024,
		Release: api.ReleaseInfo{
			Year: 2020,
		},
	})
	if year != 2024 {
		t.Fatalf("expected arr year 2024, got %d", year)
	}
}
