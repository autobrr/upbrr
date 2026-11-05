// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type keptTMDBClient struct {
	stubTMDB
	read func(context.Context, int) (int, int, error)
}

func (c *keptTMDBClient) GetSeriesExternalIDs(ctx context.Context, id int) (int, int, error) {
	return c.read(ctx, id)
}

type keptTVmazeClient struct {
	stubTVmaze
	read func(context.Context, int) (int, int, error)
}

func (c *keptTVmazeClient) GetSeriesExternalIDs(ctx context.Context, id int) (int, int, error) {
	return c.read(ctx, id)
}

func TestKeptSeriesMatchesResolveOnlyMissingSiblingsWithoutReplacingMetadata(t *testing.T) {
	for _, provider := range []string{"TMDB", "TVmaze"} {
		for _, mode := range []string{"agreement", "conflict", "missing TVDB", "cleared IMDb", "complete", "failure", "cancel"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
				input := preparationstate.State{
					SourcePath:        source,
					StoredDataFresh:   true,
					MediaInfoCategory: "TV",
					Identity: api.ExternalIdentity{
						SourcePath: source,
						Category:   api.CanonicalCategoryTV,
						TVDBID:     303,
					},
					ProviderMetadata:    api.SourceScopedMetadata{SourcePath: source},
					ExternalIDOverrides: api.ExternalIDOverrides{MALID: new(0), TVDBID: new(303)},
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reads := 0
				read := func(_ context.Context, _ int) (int, int, error) {
					reads++
					if mode == "cancel" {
						cancel()
						return 0, 0, context.Canceled
					}
					if mode == "failure" {
						return 0, 0, errors.New("provider unavailable")
					}
					if mode == "conflict" {
						return 999, 303, nil
					}
					return 202, 303, nil
				}
				tmdbClient, tvmazeClient := &keptTMDBClient{read: read}, &keptTVmazeClient{read: read}
				if provider == "TMDB" {
					input.Identity.TMDBID = 101
					input.ExternalIDOverrides.KeepTMDB = new(true)
					input.ExternalIDOverrides.TVmazeID = new(0)
					input.ProviderMetadata.TMDB = &api.TMDBMetadata{
						TMDBID:   101,
						Title:    "Kept TMDB Title",
						Category: "TV",
						IMDBID:   888,
						TVDBID:   888,
					}
				} else {
					input.Identity.TVmazeID = 404
					input.ExternalIDOverrides.KeepTVmaze = new(true)
					input.ExternalIDOverrides.TMDBID = new(0)
					input.ProviderMetadata.TVmaze = &api.TVmazeMetadata{
						TVmazeID: 404,
						Name:     "Kept TVmaze Title",
						IMDBID:   888,
						TVDBID:   888,
					}
				}
				if mode == "missing TVDB" {
					input.Identity.TVDBID = 0
					input.ExternalIDOverrides.TVDBID = nil
				}
				if mode == "cleared IMDb" {
					input.ExternalIDOverrides.IMDBID = new(0)
				}
				if mode == "complete" {
					input.ExternalIDOverrides.IMDBID = new(202)
				}
				beforeTMDB, beforeTVmaze := input.ProviderMetadata.TMDB, input.ProviderMetadata.TVmaze
				tvdbClient := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{
					TVDBID: 303,
					Name:   "Example Series",
					RemoteLinks: api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusComplete, Links: []api.TVDBRemoteLink{{
						Provider: api.IdentityProviderIMDB,
						ID:       202,
						State:    "valid",
					}}},
				}}
				imdbClient := &stubIMDB{info: imdb.Info{
					IMDbID: "tt0000202",
					Title:  "Example Series",
					Type:   "tvSeries",
				}}
				service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithTVmazeClient(tvmazeClient), WithTVDBClient(tvdbClient), WithIMDBClient(imdbClient))
				result, err := service.resolveExternalIdentity(ctx, input)
				if mode == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled lookup published a result: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 202
				if mode == "cleared IMDb" || mode == "conflict" || mode == "failure" {
					want = 0
				}
				if result.Identity.IMDBID != want || result.Identity.TVDBID != 303 {
					t.Fatalf("missing or unsafe sibling identity: %+v", result.Identity)
				}
				wantReads := 1
				if mode == "cleared IMDb" || mode == "complete" {
					wantReads = 0
				}
				if reads != wantReads || tmdbClient.metaCalls != 0 || tvmazeClient.calls != 0 {
					t.Fatalf("kept metadata fetched or identity reads repeated: links=%d TMDB=%d TVmaze=%d", reads, tmdbClient.metaCalls, tvmazeClient.calls)
				}
				if !reflect.DeepEqual(beforeTMDB, result.ProviderMetadata.TMDB) || !reflect.DeepEqual(beforeTVmaze, result.ProviderMetadata.TVmaze) {
					t.Fatal("identity-only reconciliation changed kept naming metadata")
				}
			})
		}
	}
}

func TestKeptSeriesRawLinksReuseEndpointEvidenceAcrossRestart(t *testing.T) {
	for _, provider := range []string{"TMDB", "TVmaze"} {
		t.Run(provider, func(t *testing.T) {
			requests := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				body := `{"imdb_id":"tt0000202","tvdb_id":303}`
				if provider == "TMDB" {
					if r.URL.Path != "/3/tv/101/external_ids" || r.URL.Query().Get("api_key") != "synthetic-key" || len(r.URL.Query()) != 1 {
						t.Error("TMDB identity lookup changed endpoint or filters")
					}
				} else {
					if r.URL.Path != "/shows/101" || r.URL.RawQuery != "" {
						t.Error("TVmaze identity lookup fetched unrelated metadata")
					}
					body = `{"id":101,"name":"Example Series","externals":{"imdb":"tt0000202","thetvdb":303}}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})}
			path := filepath.Join(t.TempDir(), "evidence.sqlite")
			source := filepath.Join(t.TempDir(), "Example.Series.mkv")
			for range 2 {
				repo, err := db.OpenContext(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.MigrateContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessReuse, api.NopLogger{})
				var client seriesExternalIDsClient = tmdb.NewClient(httpClient, nil, "synthetic-key")
				if provider == "TVmaze" {
					client = tvmaze.NewClient(httpClient, nil)
				}
				for range 2 {
					imdbID, tvdbID, err := client.GetSeriesExternalIDs(ctx, 101)
					if err != nil || imdbID != 202 || tvdbID != 303 {
						t.Fatalf("identity evidence lost: %d/%d %v", imdbID, tvdbID, err)
					}
				}
				if err := scope.Err(); err != nil {
					t.Fatal(err)
				}
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if requests != 1 {
				t.Fatalf("legacy miss/restart repeated endpoint HTTP: %d", requests)
			}
		})
	}
}

func TestKeptSeriesLinkWaitsForOtherSelectedProviderBeforeInference(t *testing.T) {
	for _, kept := range []string{"TMDB", "TVmaze"} {
		t.Run(kept, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
			input := preparationstate.State{
				SourcePath:        source,
				StoredDataFresh:   true,
				MediaInfoCategory: "TV",
				Identity: api.ExternalIdentity{
					SourcePath: source,
					Category:   api.CanonicalCategoryTV,
					TMDBID:     101,
					TVmazeID:   404,
				},
				ProviderMetadata: api.SourceScopedMetadata{SourcePath: source},
				ExternalIDOverrides: api.ExternalIDOverrides{
					TMDBID:   new(101),
					TVmazeID: new(404),
					TVDBID:   new(0),
					MALID:    new(0),
				},
			}
			read := func(context.Context, int) (int, int, error) { return 202, 0, nil }
			tmdbClient := &keptTMDBClient{read: read, metadata: tmdb.MetadataResult{
				Title:          "Selected TMDB",
				TMDBType:       "TV",
				IMDbID:         999,
				ExternalIMDbID: 999,
			}}
			tvmazeClient := &keptTVmazeClient{read: read, result: tvmaze.SearchResult{
				SelectedID: 404,
				IMDBID:     999,
				Candidates: []tvmaze.Candidate{{
					ID:        404,
					Name:      "Selected TVmaze",
					Externals: tvmaze.Externals{IMDB: "tt0000999"},
				}},
			}}
			if kept == "TMDB" {
				input.ExternalIDOverrides.KeepTMDB = new(true)
				input.ProviderMetadata.TMDB = &api.TMDBMetadata{
					TMDBID:   101,
					Title:    "Kept TMDB",
					Category: "TV",
				}
			} else {
				input.ExternalIDOverrides.KeepTVmaze = new(true)
				input.ProviderMetadata.TVmaze = &api.TVmazeMetadata{TVmazeID: 404, Name: "Kept TVmaze"}
			}
			service := newTestService(t, &fakeRepo{}, WithTMDBClient(tmdbClient), WithTVmazeClient(tvmazeClient), WithTVDBClient(&stubTVDB{}), WithIMDBClient(&stubIMDB{}))
			result, err := service.resolveExternalIdentity(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Identity.IMDBID != 0 || result.Identity.TMDBID != 101 || result.Identity.TVmazeID != 404 || result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TVmaze == nil {
				t.Fatalf("early kept link escaped selected-resource conflict: %+v", result.Identity)
			}
			if kept == "TMDB" && !reflect.DeepEqual(input.ProviderMetadata.TMDB, result.ProviderMetadata.TMDB) ||
				kept == "TVmaze" && !reflect.DeepEqual(input.ProviderMetadata.TVmaze, result.ProviderMetadata.TVmaze) {
				t.Fatal("conflict replaced kept naming metadata")
			}
		})
	}
}

func TestPreparedKeptTMDBReplaysRawLinksAfterIMDbAutoAndRestart(t *testing.T) {
	fixture := newCorrectionEvidenceFixture(t)
	fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002","tvdb_id":555003}`
	fixture.imdb = &stubIMDB{info: imdb.Info{
		IMDbID: "tt0555002",
		Title:  "Example Series",
		Type:   "tvSeries",
	}}
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	before := fixture.requests()
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
		Values:      api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{KeepTMDB: new(true)}},
		ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityIMDB}},
	}}
	changed := prepareCorrectionEvidence(t, module, input, update)
	if changed.Release.Identity.IMDBID != 555002 || changed.Release.ProviderMetadata.IMDB == nil ||
		!reflect.DeepEqual(initial.Release.ProviderMetadata.TMDB, changed.Release.ProviderMetadata.TMDB) || !reflect.DeepEqual(before, fixture.requests()) {
		t.Fatal("Auto failed to resolve IMDb from kept TMDB raw evidence without new HTTP or changed naming")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, _ = fixture.open(t)
	input.ExternalFreshness = api.ExternalFreshnessLoad
	restarted := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if restarted.Release.Identity.IMDBID != 555002 || fixture.requests()["/3/tv/555001/external_ids"] != before["/3/tv/555001/external_ids"] ||
		fixture.requests()["/3/tv/555001"] != before["/3/tv/555001"] {
		t.Fatalf("restart repeated kept provider HTTP or lost the linked sibling: IDs=%+v before=%v after=%v", restarted.Release.Identity, before, fixture.requests())
	}
}
