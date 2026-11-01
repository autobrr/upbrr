// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubArrClient struct {
	result ArrLookupResult
	err    error
	calls  int
	last   preparationstate.State
}

func (s *stubArrClient) Lookup(_ context.Context, meta preparationstate.State) (ArrLookupResult, error) {
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
	svc := newTestService(t,
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

	meta, err := svc.collectArrIdentityEvidence(context.Background(), preparationstate.State{
		SourcePath: "/data/Show.S01E01.mkv",
		SeasonInt:  1,
		EpisodeInt: 1,
		Release: api.ReleaseInfo{
			Title: "Show",
		},
	})
	if err != nil {
		t.Fatalf("collectArrIdentityEvidence returned error: %v", err)
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

func TestArrSelectionNormalizesRetainedIdentityBeforeProviderResolution(t *testing.T) {
	for _, test := range []struct {
		name      string
		version   string
		freshness api.ExternalFreshness
		reset     bool
		manual    bool
		wantTV    bool
	}{
		{
			name:      "obsolete automatic TV",
			version:   "external-identity-v6",
			freshness: api.ExternalFreshnessLoad,
		},
		{
			name:      "refresh automatic TV",
			version:   externalidentity.ContractVersion,
			freshness: api.ExternalFreshnessRefresh,
		},
		{
			name:      "reuse current automatic TV",
			version:   externalidentity.ContractVersion,
			freshness: api.ExternalFreshnessReuse,
			wantTV:    true,
		},
		{
			name:      "refresh preserves manual TV pins and clears",
			version:   "external-identity-v6",
			freshness: api.ExternalFreshnessRefresh,
			manual:    true,
			wantTV:    true,
		},
		{
			name:      "Auto category resets lookup anchors",
			version:   externalidentity.ContractVersion,
			freshness: api.ExternalFreshnessReuse,
			reset:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "Example.Movie.2026.1080p-GRP.mkv")
			meta := preparationstate.State{
				SourcePath:        source,
				StoredDataFresh:   true,
				ExternalFreshness: test.freshness,
				Release:           api.ReleaseInfo{Title: "Example Movie", Category: "MOVIE"},
				MediaInfoCategory: "MOVIE",
				MALID:             777099,
				Identity: api.ExternalIdentity{
					SourcePath: source,
					Category:   api.CanonicalCategoryTV,
					TMDBID:     777001,
					IMDBID:     777002,
					TVDBID:     777003,
					MALID:      777099,
					Resolution: api.IdentityResolutionKey{ContractVersion: test.version},
				},
			}
			if test.manual {
				meta.Identity.IMDBID = 0
				meta.ReleaseNameOverrides.Category = new("TV")
				meta.Identity.Provenance = api.IdentityProvenanceSet{
					Category: api.IdentityProvenanceExplicit,
					TVDB:     api.IdentityProvenanceExplicit,
					IMDB:     api.IdentityProvenanceExplicit,
				}
				meta.Identity.Overrides = api.IdentityOverrideState{
					Category: api.OverrideStateValue,
					TVDB:     api.OverrideStateValue,
					IMDB:     api.OverrideStateClear,
				}
			}
			if test.reset {
				meta.Identity.Provenance.Category = api.IdentityProvenanceExplicit
				meta.Identity.Overrides.Category = api.OverrideStateValue
				meta.IdentityResetFields = []api.CorrectionField{api.CorrectionFieldReleaseNameCategory}
			}
			sonarr := &stubArrClient{result: ArrLookupResult{
				Source: "sonarr",
				TMDBID: 777001,
				IMDBID: 777002,
				TVDBID: 777003,
			}}
			radarr := &stubArrClient{result: ArrLookupResult{
				Source: "radarr",
				TMDBID: 555001,
				IMDBID: 555002,
			}}
			tmdbClient := &stubTMDB{
				findResult: tmdb.FindResult{TMDBID: 777001, Category: "TV"},
				metadataFn: func(input tmdb.MetadataInput) (tmdb.MetadataResult, error) {
					if input.Category == "tv" {
						return tmdb.MetadataResult{
							Title:          "Example Series",
							TMDBType:       "Scripted",
							TVDBID:         777003,
							ExternalTVDBID: 777003,
						}, nil
					}
					return tmdb.MetadataResult{Title: "Example Movie", TMDBType: "Movie"}, nil
				},
			}
			svc := newTestService(t, &fakeRepo{ids: meta.Identity},
				WithConfig(config.Config{ArrIntegration: config.ArrIntegrationConfig{UseSonarr: true, UseRadarr: true}}),
				WithSonarrClient(sonarr), WithRadarrClient(radarr), WithTMDBClient(tmdbClient),
				WithIMDBClient(&stubIMDB{}), WithTVDBClient(&stubTVDB{name: "Example Series"}), WithTVmazeClient(&stubTVmaze{}))
			collected, err := svc.collectArrIdentityEvidence(t.Context(), meta)
			if err != nil {
				t.Fatal(err)
			}
			selected := radarr
			wantCategory, wantTMDB, wantIMDb := api.CanonicalCategoryMovie, 555001, 555002
			if test.wantTV {
				selected = sonarr
				wantCategory, wantTMDB, wantIMDb = api.CanonicalCategoryTV, 777001, 777002
			}
			if selected.calls != 1 || sonarr.calls+radarr.calls != 1 {
				t.Fatalf("wrong Arr selected: sonarr=%d radarr=%d", sonarr.calls, radarr.calls)
			}
			if !test.wantTV && (selected.last.Identity.TMDBID != 0 || selected.last.Identity.IMDBID != 0 || selected.last.Identity.TVDBID != 0 || collected.MALID != 0) {
				t.Fatal("Movie lookup received obsolete TV lookup anchors")
			}
			if test.manual {
				wantIMDb = 0
				if selected.last.Identity.TVDBID != 777003 || selected.last.Identity.Overrides.IMDB != api.OverrideStateClear {
					t.Fatal("Arr normalization discarded manual pins or clears")
				}
			}
			resolved, err := svc.collectExternalIdentityEvidence(t.Context(), collected)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Identity.Category != wantCategory || resolved.Identity.TMDBID != wantTMDB || resolved.Identity.IMDBID != wantIMDb ||
				resolved.ProviderMetadata.TMDB == nil || resolved.ProviderMetadata.TMDB.Category != string(wantCategory) || tmdbClient.searchCalls != 0 {
				t.Fatalf("Arr evidence crossed category namespaces during resolution: %+v", resolved.Identity)
			}
		})
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
		info: imdb.Info{
			IMDbID: "tt0000123",
			Title:  "Example",
			Year:   2021,
		},
	}
	svc := newTestService(t,
		repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "token"}}),
		WithTMDBClient(tmdbClient),
		WithIMDBClient(imdbClient),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
		SourcePath: "/data/Example.2021.mkv",
		ArrSource:  "radarr",
		ArrTMDBID:  123,
		ArrIMDBID:  456,
	})
	if err != nil {
		t.Fatalf("resolveExternalIdentity returned error: %v", err)
	}
	if result.Identity.TMDBID != 123 || result.Identity.Provenance.TMDB != api.IdentityProvenanceArr {
		t.Fatalf("expected arr tmdb id preserved, got %#v", result.Identity)
	}
	if result.Identity.IMDBID != 456 || result.Identity.Provenance.IMDB != api.IdentityProvenanceArr {
		t.Fatalf("expected arr imdb id preserved, got %#v", result.Identity)
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
	svc := newTestService(t,
		repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "token"}}),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2022}}),
		WithIMDBClient(&stubIMDB{info: imdb.Info{
			IMDbID: "tt0000777",
			Title:  "Example",
			Year:   2022,
		}}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	result, err := svc.resolveExternalIdentity(context.Background(), preparationstate.State{
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
		t.Fatalf("resolveExternalIdentity returned error: %v", err)
	}
	if result.Identity.TMDBID != overrideTMDB || result.Identity.Provenance.TMDB != api.IdentityProvenanceExplicit || result.Identity.Overrides.TMDB != api.OverrideStateValue {
		t.Fatalf("expected tmdb override retained, got %#v", result.Identity)
	}
	if result.Identity.IMDBID != overrideIMDB || result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit || result.Identity.Overrides.IMDB != api.OverrideStateValue {
		t.Fatalf("expected imdb override retained, got %#v", result.Identity)
	}
}

func TestResolveSearchYearUsesArrYearBeforeReleaseYear(t *testing.T) {
	year := resolveSearchYear(preparationstate.State{
		ArrYear: 2024,
		Release: api.ReleaseInfo{
			Year: 2020,
		},
	})
	if year != 2024 {
		t.Fatalf("expected arr year 2024, got %d", year)
	}
}
