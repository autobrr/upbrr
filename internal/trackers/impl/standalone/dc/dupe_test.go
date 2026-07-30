// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

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

func TestDuplicateSearchUsesDCQueryHeadersAndProjection(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("searchText") != "tt1234567" || r.Header.Get("X-Api-Key") != "secret" {
			requestErr <- errors.New("unexpected DC duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[{"id":42,"name":"Example.Release.2026.1080p-GRP","size":1234}]`))
	}))
	defer server.Close()

	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"DC": {APIKey: "secret"},
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
	if len(entries) != 1 || entries[0].ID != "42" || entries[0].Link != "https://digitalcore.club/torrent/42/" || entries[0].SizeBytes != 1234 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}
