// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type tlRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tlRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestDuplicateSearchTitleFallbackHonorsManualTitle(t *testing.T) {
	t.Parallel()

	requests := 0
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {Passkey: "passkey"}}}},
		http: &http.Client{Transport: tlRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if !strings.HasSuffix(request.URL.Path, "/Projected Release") {
				t.Errorf("automatic TL search path = %q", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"numFound":0,"page":1,"perPage":100,"torrentList":[]}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Projection:        projection,
		EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
	})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata || requests != 0 {
		t.Fatalf("manual-empty TL result=%v code=%q requests=%d", result.Disposition(), result.Code(), requests)
	}
	if result := searcher.Search(context.Background(), api.DuplicateSubject{Projection: projection}); result.Disposition() != dupe.DispositionResolved || requests != 1 {
		t.Fatalf("automatic TL result=%v requests=%d", result.Disposition(), requests)
	}
}

// All pages are synthetic; only the first/final metadata shapes were supplied live.
func TestIMDbSearchPagination(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                 string
		total, perPage, imdb, pages, entries int
		change                               func(int, map[string]any)
		failPage                             int
		limit                                int
		complete                             bool
	}{
		{
			name:     "complete first page restores leading zeroes",
			total:    42,
			perPage:  100,
			imdb:     456,
			pages:    1,
			entries:  42,
			complete: true,
		},
		{
			name:     "four pages preserve long identity",
			total:    320,
			perPage:  100,
			imdb:     12345678,
			pages:    4,
			entries:  320,
			complete: true,
		},
		{
			name:    "registered page limit",
			total:   320,
			perPage: 100,
			imdb:    456,
			limit:   2,
			pages:   2,
			entries: 200,
		},
		{
			name:     "complete at registered page limit",
			total:    200,
			perPage:  100,
			imdb:     456,
			limit:    2,
			pages:    2,
			entries:  200,
			complete: true,
		},
		{
			name:    "registered limit retains hard ceiling",
			total:   101,
			perPage: 1,
			imdb:    456,
			limit:   200,
			pages:   100,
			entries: 100,
		},
		{
			name:     "later failure keeps first page",
			total:    320,
			perPage:  100,
			imdb:     456,
			failPage: 2,
			pages:    2,
			entries:  100,
		},
		{
			name:    "missing rows on last page",
			total:   320,
			perPage: 100,
			imdb:    456,
			pages:   4,
			entries: 319,
			change: func(page int, body map[string]any) {
				if page == 4 {
					rows, ok := body["torrentList"].([]map[string]any)
					if !ok {
						t.Fatal("synthetic page lacks typed rows")
					}
					body["torrentList"] = rows[1:]
				}
			},
		},
		{
			name:    "wrong returned page",
			total:   320,
			perPage: 100,
			imdb:    456,
			pages:   2,
			entries: 200,
			change: func(page int, body map[string]any) {
				if page == 2 {
					body["page"] = 1
				}
			},
		},
		{
			name:    "changed page size",
			total:   320,
			perPage: 100,
			imdb:    456,
			pages:   2,
			entries: 200,
			change: func(page int, body map[string]any) {
				if page == 2 {
					body["perPage"] = 101
				}
			},
		},
		{
			name:    "changed total",
			total:   320,
			perPage: 100,
			imdb:    456,
			pages:   2,
			entries: 200,
			change: func(page int, body map[string]any) {
				if page == 2 {
					body["numFound"] = 300
				}
			},
		},
		{
			name:    "repeated IDs stop without progress",
			total:   320,
			perPage: 100,
			imdb:    456,
			pages:   2,
			entries: 100,
			change: func(_ int, body map[string]any) {
				rows, ok := body["torrentList"].([]map[string]any)
				if !ok {
					t.Fatal("synthetic page lacks typed rows")
				}
				for index, row := range rows {
					row["fid"] = strconv.Itoa(index + 1)
				}
			},
		},
		{
			name:    "bounded total requests",
			total:   101,
			perPage: 1,
			imdb:    456,
			pages:   100,
			entries: 100,
		},
		{
			name:     "explicit empty response",
			total:    0,
			perPage:  100,
			imdb:     456,
			pages:    1,
			entries:  0,
			complete: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			searcher := testSearcher(func(request *http.Request) (*http.Response, error) {
				requests++
				identity := "tt0000456"
				if test.imdb == 12345678 {
					identity = "tt12345678"
				}
				want := "/torrents/browse/list/imdbID/" + identity
				if requests > 1 {
					want += "/page/" + strconv.Itoa(requests)
				}
				if request.URL.Path != want {
					t.Fatalf("request path = %q, want %q", request.URL.Path, want)
				}
				if requests == test.failPage {
					return nil, errors.New("synthetic request failure")
				}
				rows := make([]map[string]any, 0)
				for index := (requests - 1) * test.perPage; index < min(requests*test.perPage, test.total); index++ {
					rows = append(rows, map[string]any{
						"fid":      strconv.Itoa(index + 1),
						"name":     fmt.Sprintf("Example.Show.S01E%02d.1080p.WEB-DL-GRP", index+1),
						"imdbID":   identity,
						"tvmazeID": "e123",
						"filename": "release.torrent",
						"size":     1024,
					})
				}
				body := map[string]any{
					"torrentList": rows,
					"numFound":    test.total,
					"page":        requests,
					"perPage":     test.perPage,
				}
				if test.change != nil {
					test.change(requests, body)
				}
				return jsonResponse(t, body), nil
			})
			var adapter dupe.Adapter = searcher
			if test.limit > 0 {
				profile := Profile()
				profile.DupePolicy.SearchScope.MaxPages = test.limit
				definition := standalone.MustNew(profile)
				registry := trackers.NewRegistry()
				if err := registry.Register(definition); err != nil {
					t.Fatal(err)
				}
				adapter = dupe.NewAdapter(definition, "TL", searcher.cfg, searcher.http, api.NopLogger{}, registry)
			}
			result := adapter.Search(t.Context(), api.DuplicateSubject{
				Identity:         api.ExternalIdentity{IMDBID: test.imdb}, // No title; conflicting provider cross-links cannot change this request.
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{IMDBID: 999}},
			})
			if result.Disposition() != dupe.DispositionResolved || requests != test.pages || len(result.Entries()) != test.entries || result.SearchEvidence().EffectiveComplete() != test.complete {
				t.Fatalf("result=%v requests=%d entries=%d evidence=%+v", result.Disposition(), requests, len(result.Entries()), result.SearchEvidence())
			}
			if !test.complete && len(result.SearchEvidence().Warnings) == 0 {
				t.Fatal("partial result lacks a warning")
			}
		})
	}
}

func TestIMDbSearchBindingAndFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body     string
		failed         bool
		entries, wrong int
	}{
		{
			name:  "wrong work excluded",
			body:  `{"numFound":1,"page":1,"perPage":100,"torrentList":[{"fid":"1","name":"Other.Movie.2026","imdbID":"tt0000999"}]}`,
			wrong: 1,
		},
		{
			name:    "missing identity retained",
			body:    `{"numFound":1,"page":1,"perPage":100,"torrentList":[{"fid":"1","name":"Example.Movie.2026"}]}`,
			entries: 1,
		},
		{
			name:    "missing pagination retained",
			body:    `{"torrentList":[{"fid":"1","name":"Example.Movie.2026","imdbID":"tt0000456"}]}`,
			entries: 1,
		},
		{
			name:   "missing empty pagination fails",
			body:   `{"torrentList":[]}`,
			failed: true,
		},
		{
			name:   "missing list fails",
			body:   `{}`,
			failed: true,
		},
		{
			name:   "invalid JSON fails",
			body:   `<html>login</html>`,
			failed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			searcher := testSearcher(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			result := searcher.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 456}})
			if (result.Disposition() == dupe.DispositionFailed) != test.failed || len(result.Entries()) != test.entries || result.SearchEvidence().EffectiveComplete() || result.SearchEvidence().WrongWorkCount != test.wrong {
				t.Fatalf("result=%v entries=%d evidence=%+v", result.Disposition(), len(result.Entries()), result.SearchEvidence())
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	searcher := testSearcher(func(*http.Request) (*http.Response, error) { cancel(); return nil, ctx.Err() })
	if result := searcher.Search(ctx, api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 456}}); result.Disposition() != dupe.DispositionFailed || !errors.Is(result.Cause(), context.Canceled) {
		t.Fatalf("cancellation = %+v", result)
	}
}

func TestTitleFallbackIncludesEpisodeRangesAndPacks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		foreign, anime bool
		requests       int
		category       string
	}{
		{
			name:     "English",
			requests: 2,
			category: "32",
		},
		{
			name:     "foreign deduplicates scopes",
			foreign:  true,
			requests: 1,
			category: "44",
		},
		{
			name:     "anime deduplicates scopes",
			anime:    true,
			requests: 1,
			category: "34",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			searcher := testSearcher(func(request *http.Request) (*http.Response, error) {
				requests++
				category := test.category
				if requests == 2 {
					category = "27"
				}
				want := "/torrents/browse/list/categories/" + category + "/query/Example Show S01 1080p"
				if request.URL.Path != want {
					t.Fatalf("path = %q, want %q", request.URL.Path, want)
				}
				return jsonResponse(t, map[string]any{
					"numFound": 2,
					"page":     1,
					"perPage":  100,
					"torrentList": []map[string]any{
						{"fid": "1", "name": "Example.Show.S01E01-E03.1080p.WEB-DL-GRP"},
						{"fid": "2", "name": "Example.Show.S01.1080p.WEB-DL-GRP"},
					},
				}), nil
			})
			meta := api.DuplicateSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
				Release:    api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
				SeasonInt:  1,
				SeasonStr:  "S01",
				EpisodeInt: 2,
				EpisodeStr: "E02",
				Anime:      test.anime,
			}
			if test.foreign {
				meta.EffectiveMetadata.OriginalLanguage = "fr"
			}
			result := searcher.Search(t.Context(), meta)
			if requests != test.requests || len(result.Entries()) != 2 || result.SearchEvidence().EffectiveComplete() || !result.SearchEvidence().Complete {
				t.Fatalf("requests=%d entries=%d search=%+v", requests, len(result.Entries()), result.SearchEvidence())
			}
			candidates := make([]dupe.TrackerCandidate, 0, 2)
			for _, entry := range result.Entries() {
				candidates = append(candidates, dupe.NormalizeCandidate(entry, "TL"))
			}
			evaluation := dupe.Evaluate(api.TrackerDuplicateTarget{
				Category:   "TV",
				Season:     1,
				Episode:    2,
				Type:       "WEBDL",
				Resolution: "1080p",
			}, candidates, *Profile().DupePolicy, result.SearchEvidence())
			for _, candidate := range evaluation.Candidates {
				if candidate.Relation == api.DupeRelationCoexists {
					t.Fatalf("overlapping content discarded: %+v", candidate)
				}
			}
		})
	}
}

func testSearcher(transport tlRoundTripFunc) *dupeSearcher {
	return &dupeSearcher{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {Passkey: "synthetic"}}}}, http: &http.Client{Transport: transport}}
}

func jsonResponse(t *testing.T, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(payload)))}
}
