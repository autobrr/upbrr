// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateSearchUsesDCDupeEndpointAndProjection(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if err := assertDCQuery(query, map[string]string{
			"imdb":        "tt0000456",
			"releaseName": "Example.Release.2026.1080p.WEB-DL-GRP",
			"limit":       "100",
			"index":       "0",
		}); err != nil {
			requestErr <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Api-Key") != "secret" || r.Header.Get("Accept") != "application/json" {
			requestErr <- errors.New("unexpected DC duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":42,"name":"Example.Release.2026.1080p.WEB-DL-GRP","size":1234,"categoryName":"Movies/1080p","type":"single","numfiles":2,"p2p":true,"pack":false,"3d":true,"approved":false,"pending":true,"status":"pending"}],"index":0,"limit":100,"count":1,"total":1,"includesPending":true}`))
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 456},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p.WEB-DL-GRP"},
		},
	})
	select {
	case err := <-requestErr:
		t.Fatal(err)
	default:
	}
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	entries := result.Entries()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	entry := entries[0]
	if entry.ID != "42" || entry.Link != "https://digitalcore.club/torrent/42/" || entry.SizeBytes != 1234 || !entry.SizeKnown {
		t.Fatalf("unexpected identity/size: %#v", entry)
	}
	if entry.Category != "Movies/1080p" || entry.Type != "" || entry.Res != "1080p" {
		t.Fatalf("unexpected category/type/resolution: %#v", entry)
	}
	if entry.FileCount != 2 || entry.Pack || entry.ThreeD != "3d" || entry.ReleaseOrigin != "p2p" {
		t.Fatalf("unexpected flags: %#v", entry)
	}
	if entry.Attributes["status"] != "pending" || entry.Attributes["approved"] != "false" || entry.Attributes["pending"] != "true" {
		t.Fatalf("unexpected status attributes: %#v", entry.Attributes)
	}
	if search := result.SearchEvidence(); !search.Complete || search.WorkScope != dupe.WorkScopeProviderID || !search.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchFallsBackToReleaseNameWithoutIMDb(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Has("imdb") {
			requestErr <- errors.New("unexpected imdb query for title fallback")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := assertDCQuery(query, map[string]string{
			"releaseName": "Title.Only.2026.720p-GRP",
			"limit":       "100",
			"index":       "0",
		}); err != nil {
			requestErr <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"index":0,"limit":100,"count":0,"total":0,"includesPending":true}`))
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), api.DuplicateSubject{ReleaseName: "Title.Only.2026.720p-GRP"})
	select {
	case err := <-requestErr:
		t.Fatal(err)
	default:
	}
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if entries := result.Entries(); len(entries) != 0 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	if search := result.SearchEvidence(); !search.Complete || search.WorkScope != dupe.WorkScopeTitle || search.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchPaginatesUntilTerminalTotal(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("index") {
		case "0":
			requests++
			if !writeDCDupePage(t, w, dcTestPage{
				Index:           0,
				Count:           100,
				Total:           101,
				IncludesPending: boolPtr(true),
			}) {
				return
			}
		case "100":
			requests++
			if !writeDCDupePage(t, w, dcTestPage{
				Index:           100,
				Count:           1,
				Total:           101,
				IncludesPending: boolPtr(true),
			}) {
				return
			}
		default:
			t.Errorf("unexpected index query %q", r.URL.Query().Get("index"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{IMDBID: 456},
		ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
	})
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if entries := result.Entries(); len(entries) != 101 {
		t.Fatalf("entries = %d, want 101", len(entries))
	}
	if search := result.SearchEvidence(); !search.Complete || !search.EffectiveComplete() || search.Pages != 2 || len(search.Warnings) != 0 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchRequiresPendingCoverageForCompleteness(t *testing.T) {
	tests := []struct {
		name            string
		includesPending *bool
	}{
		{name: "false", includesPending: boolPtr(false)},
		{name: "missing", includesPending: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !writeDCDupePage(t, w, dcTestPage{
					Index:           0,
					Count:           1,
					Total:           1,
					IncludesPending: tc.includesPending,
				}) {
					return
				}
			}))
			defer server.Close()

			searcher := testDCDupeSearcher(server)
			result := searcher.Search(context.Background(), api.DuplicateSubject{
				Identity:    api.ExternalIdentity{IMDBID: 456},
				ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
			})
			if result.Disposition() != dupe.DispositionResolved {
				t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
			}
			if entries := result.Entries(); len(entries) != 0 {
				t.Fatalf("invalid pending-coverage page leaked entries: %#v", entries)
			}
			if search := result.SearchEvidence(); search.Complete || search.EffectiveComplete() || len(search.Warnings) != 2 {
				t.Fatalf("unexpected search evidence: %#v", search)
			}
		})
	}
}

func TestDuplicateSearchRejectsInconsistentPagination(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Query().Get("index") {
		case "0":
			if !writeDCDupePage(t, w, dcTestPage{
				Index:           0,
				Count:           100,
				Total:           101,
				IncludesPending: boolPtr(true),
			}) {
				return
			}
		case "100":
			if !writeDCDupePage(t, w, dcTestPage{
				Index:           100,
				Count:           1,
				Total:           102,
				IncludesPending: boolPtr(true),
			}) {
				return
			}
		default:
			t.Errorf("unexpected index query %q", r.URL.Query().Get("index"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{IMDBID: 456},
		ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
	})
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if search := result.SearchEvidence(); search.Complete || search.EffectiveComplete() || search.Pages != 2 || len(search.Warnings) != 1 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchRejectsNonProgressingPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !writeDCDupePage(t, w, dcTestPage{
			Index:           0,
			Count:           0,
			Total:           1,
			IncludesPending: boolPtr(true),
		}) {
			return
		}
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{IMDBID: 456},
		ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
	})
	if search := result.SearchEvidence(); search.Complete || search.EffectiveComplete() || search.Pages != 1 || len(search.Warnings) != 1 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchPaginationBoundFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !writeDCDupePage(t, w, dcTestPage{
			Index:           0,
			Count:           100,
			Total:           101,
			IncludesPending: boolPtr(true),
		}) {
			return
		}
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	searcher.maxPages = 1
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Identity:    api.ExternalIdentity{IMDBID: 456},
		ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
	})
	if search := result.SearchEvidence(); search.Complete || search.EffectiveComplete() || search.Pages != 1 || len(search.Warnings) != 1 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchClassifiesRequestFailure(t *testing.T) {
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"DC": {APIKey: "secret"},
		}}},
		http:     http.DefaultClient,
		endpoint: "://bad-dc-url",
		maxPages: dcDupeMaxPages,
	}
	result := searcher.Search(context.Background(), testDCDupeSubject())
	assertDCDupeFailed(t, result, dupe.FailureRequest)
	if result.Cause() == nil {
		t.Fatal("expected request failure cause")
	}
}

func TestDuplicateSearchClassifiesResponseStatusFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	searcher := testDCDupeSearcher(server)
	result := searcher.Search(context.Background(), testDCDupeSubject())
	assertDCDupeFailed(t, result, dupe.FailureResponseStatus)
	cause := result.Cause()
	if cause == nil || !strings.Contains(cause.Error(), "401") || strings.Contains(cause.Error(), server.URL) {
		t.Fatalf("unexpected status cause: %v", cause)
	}
}

func TestDuplicateSearchClassifiesResponseParseFailures(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantCause string
	}{
		{
			name: "malformed",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"results":`))
			},
			wantCause: "decode DC duplicate response",
		},
		{
			name: "oversized",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", dcDupeMaxResponseBytes+1)))
			},
			wantCause: "exceeds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()

			searcher := testDCDupeSearcher(server)
			result := searcher.Search(context.Background(), testDCDupeSubject())
			assertDCDupeFailed(t, result, dupe.FailureResponseParse)
			if cause := result.Cause(); cause == nil || !strings.Contains(cause.Error(), test.wantCause) {
				t.Fatalf("unexpected parse cause: %v", cause)
			}
		})
	}
}

func TestDuplicateSearchRequiresIMDbOrReleaseName(t *testing.T) {
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"DC": {APIKey: "secret"},
		}}},
		http:     http.DefaultClient,
		endpoint: "http://example.invalid",
	}
	result := searcher.Search(context.Background(), api.DuplicateSubject{})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata {
		t.Fatalf("unexpected result: disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
}

func testDCDupeSearcher(server *httptest.Server) *dupeSearcher {
	return &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"DC": {APIKey: "secret"},
		}}},
		http:     server.Client(),
		endpoint: server.URL,
		maxPages: dcDupeMaxPages,
	}
}

func testDCDupeSubject() api.DuplicateSubject {
	return api.DuplicateSubject{
		Identity:    api.ExternalIdentity{IMDBID: 456},
		ReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
	}
}

func assertDCDupeFailed(t *testing.T, result dupe.AdapterResult, wantCode string) {
	t.Helper()
	if result.Disposition() != dupe.DispositionFailed || result.Code() != wantCode {
		t.Fatalf("unexpected result disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if entries := result.Entries(); len(entries) != 0 {
		t.Fatalf("failed search returned entries: %#v", entries)
	}
	if search := result.SearchEvidence(); search.Complete || search.EffectiveComplete() || search.Pages != 0 {
		t.Fatalf("failed search claimed completeness: %#v", search)
	}
}

func assertDCQuery(query url.Values, want map[string]string) error {
	for key, value := range want {
		if got := query.Get(key); got != value {
			return errors.New("unexpected " + key + " query value")
		}
	}
	return nil
}

type dcTestPage struct {
	Index           int
	Count           int
	Total           int
	IncludesPending *bool
}

func writeDCDupePage(t *testing.T, w http.ResponseWriter, page dcTestPage) bool {
	t.Helper()
	results := make([]map[string]any, 0, page.Count)
	for i := 0; i < page.Count; i++ {
		id := page.Index + i + 1
		results = append(results, map[string]any{
			"id":           id,
			"name":         fmt.Sprintf("Example.Release.%03d.2026.1080p.WEB-DL-GRP", id),
			"categoryName": "Movies/1080p",
		})
	}
	payload := map[string]any{
		"results": results,
		"index":   page.Index,
		"limit":   dcDupePageSize,
		"count":   page.Count,
		"total":   page.Total,
	}
	if page.IncludesPending != nil {
		payload["includesPending"] = *page.IncludesPending
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode DC page %d: %v", page.Index, err)
		return false
	}
	return true
}

func boolPtr(value bool) *bool {
	return &value
}
