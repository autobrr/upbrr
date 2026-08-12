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

func TestDuplicateSearchUsesPaddedIMDbID(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("imdbID") != "tt0000456" || query.Get("api_key") != "secret" || query.Get("action") != "torrent" {
			requestErr <- errors.New("unexpected GPW duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":200,"response":[]}`))
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
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("unexpected disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if search := result.SearchEvidence(); !search.Complete || search.WorkScope != dupe.WorkScopeProviderID || !search.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
}
