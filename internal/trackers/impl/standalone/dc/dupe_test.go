// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		_, _ = w.Write([]byte(`{"results":[{"id":42,"name":"Example.Release.2026.1080p.WEB-DL-GRP","size":1234,"categoryName":"Movies/1080p","type":"single","numfiles":2,"p2p":true,"pack":false,"3d":true,"approved":false,"pending":true,"status":"pending"}],"total":1,"includesPending":true}`))
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
		}); err != nil {
			requestErr <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"total":0,"includesPending":true}`))
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
