// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateSearchResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		httpStatus int
		body       string
		wantName   string
		wantCode   string
	}{
		{
			name:     "known title",
			body:     `{"status":200,"response":[{"Name":"Example","Year":2020,"Resolution":"1080p"}]}`,
			wantName: "Example 2020 1080p",
		},
		{name: "empty success", body: `{"status":200,"response":[]}`},
		{name: "observed missing title", body: `{"status":"failure","error":"Torrent not found","response":[]}`},
		{
			name:     "success with API error",
			body:     `{"status":200,"error":"Invalid key","response":[]}`,
			wantCode: dupe.FailureResponseStatus,
		},
		{
			name:     "unknown status",
			body:     `{"status":"unknown","error":"Torrent not found","response":[]}`,
			wantCode: dupe.FailureResponseStatus,
		},
		{
			name:     "different API error",
			body:     `{"status":"failure","error":"Invalid key","response":[]}`,
			wantCode: dupe.FailureResponseStatus,
		},
		{
			name:     "contradictory missing title",
			body:     `{"status":"failure","error":"Torrent not found","response":[{"Name":"Example"}]}`,
			wantCode: dupe.FailureResponseStatus,
		},
		{
			name:     "missing title without array",
			body:     `{"status":"failure","error":"Torrent not found","response":null}`,
			wantCode: dupe.FailureResponseStatus,
		},
		{
			name:       "HTTP failure",
			httpStatus: http.StatusTooManyRequests,
			body:       `{"status":"failure","error":"Torrent not found","response":[]}`,
			wantCode:   dupe.FailureResponseStatus,
		},
		{
			name:     "missing successful response",
			body:     `{"status":200}`,
			wantCode: dupe.FailureResponseParse,
		},
		{
			name:     "malformed response",
			body:     `{"status":200,"response":{}}`,
			wantCode: dupe.FailureResponseParse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testDuplicateSearchResponse(t, tc.httpStatus, tc.body, tc.wantName, tc.wantCode)
		})
	}
}

func testDuplicateSearchResponse(t *testing.T, httpStatus int, body, wantName string, wantCode string) {
	t.Helper()
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("imdbID") != "tt0000456" || query.Get("api_key") != "secret" || query.Get("action") != "torrent" {
			requestErr <- errors.New("unexpected GPW duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if httpStatus != 0 {
			w.WriteHeader(httpStatus)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"GPW": {APIKey: "secret"},
		}}},
		http:     server.Client(),
		endpoint: server.URL,
	}
	result := searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 456}})
	select {
	case err := <-requestErr:
		t.Fatal(err)
	default:
	}
	if wantCode != "" {
		if result.Disposition() != dupe.DispositionFailed || result.Code() != wantCode || result.SearchEvidence().EffectiveComplete() {
			t.Fatalf("expected unresolved failure %q; got disposition=%v code=%q search=%#v", wantCode, result.Disposition(), result.Code(), result.SearchEvidence())
		}
		return
	}
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	entries := result.Entries()
	if wantName == "" && len(entries) != 0 || wantName != "" && (len(entries) != 1 || entries[0].Name != wantName) {
		t.Fatalf("unexpected candidate entries %#v; want %q", entries, wantName)
	}
	if search := result.SearchEvidence(); !search.Complete || search.WorkScope != dupe.WorkScopeProviderID || !search.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}
