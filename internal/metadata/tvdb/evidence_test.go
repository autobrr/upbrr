// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEpisodeEvidenceDistinguishesEmptyFromPartialResults(t *testing.T) {
	t.Parallel()
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "evidence.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				body := `{"data":{"episodes":[],"slug":"example-series"}}`
				if populated {
					body = `{"data":{"episodes":[{"id":555101,"seasonNumber":1,"number":1}],"slug":"example-series"}}`
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := &Client{
				baseURL:   server.URL,
				apiKey:    "synthetic-key",
				authToken: "synthetic-token",
				http:      server.Client(),
				logger:    api.NopLogger{},
			}
			source := filepath.Join(t.TempDir(), "Example.Series.S01E01.mkv")
			for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
				ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", freshness, nil)
				var result episodesResponse
				if err := client.getJSON(ctx, "/series/555001/episodes/official", nil, &result); err != nil || scope.Err() != nil {
					t.Fatalf("episode evidence: %v, persistence: %v", err, scope.Err())
				}
				want := 0
				if populated {
					want = 1
				}
				if len(result.Data.Episodes) != want || result.Data.Slug != "example-series" {
					t.Fatalf("replayed episode result lost facts: %+v", result)
				}
			}
			wantCalls := int32(2)
			if populated {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("requests = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}
