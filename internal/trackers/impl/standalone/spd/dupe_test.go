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
		if r.URL.Query().Get("imdbId") != "1234567" || r.Header.Get("Authorization") != "secret" || r.Header.Get("Accept") != "application/json" {
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
	result := searcher.Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
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
}
