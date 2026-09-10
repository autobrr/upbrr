// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

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

func TestDuplicateSearchUsesSPDQueryHeadersAndProjection(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("imdbId") != "tt0000456" || r.Header.Get("Authorization") != "secret" || r.Header.Get("Accept") != "application/json" {
			requestErr <- errors.New("unexpected SPD duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"84","name":"Example.Release.2026.1080p-GRP","size":4321}]`))
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"SPD": {APIKey: "secret"},
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
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	entries := result.Entries()
	if len(entries) != 1 || entries[0].ID != "84" || entries[0].Link != "https://speedapp.io/browse/84/" || entries[0].SizeBytes != 4321 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	if search := result.SearchEvidence(); !search.Complete || search.WorkScope != dupe.WorkScopeProviderID || !search.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}

func TestDuplicateSearchTitleFallbackHonorsManualTitle(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if got := request.URL.Query().Get("search"); got != "Projected Release" {
			t.Errorf("automatic SPD search title = %q", got)
		}
		_, _ = writer.Write([]byte(`[]`))
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg:      config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"SPD": {APIKey: "secret"}}}},
		http:     server.Client(),
		endpoint: server.URL,
	}
	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	result := searcher.Search(t.Context(), api.DuplicateSubject{
		Projection:        projection,
		EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
	})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata || requests != 0 {
		t.Fatalf("manual-empty SPD result=%v code=%q requests=%d", result.Disposition(), result.Code(), requests)
	}
	if result := searcher.Search(t.Context(), api.DuplicateSubject{Projection: projection}); result.Disposition() != dupe.DispositionResolved || requests != 1 {
		t.Fatalf("automatic SPD result=%v requests=%d", result.Disposition(), requests)
	}
}
