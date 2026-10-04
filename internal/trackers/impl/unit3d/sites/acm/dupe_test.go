// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestACMDuplicateSearchUsesWorkScope(t *testing.T) {
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		t.Run(string(category), func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				query := r.URL.Query()
				if query.Get("tmdb") != "42" {
					t.Errorf("ACM work parameter = %q, want 42", query.Get("tmdb"))
				}
				for _, key := range []string{"tmdbId", "name", "seasonNumber", "episodeNumber", "types[]", "resolutions[]", "api_token"} {
					if query.Has(key) {
						t.Errorf("unexpected narrowing or unsupported parameter %q", key)
					}
				}
				wantCategory := "1"
				if category == api.CanonicalCategoryTV {
					wantCategory = "2"
				}
				if query.Get("categories[]") != wantCategory {
					t.Errorf("category = %q, want %q", query.Get("categories[]"), wantCategory)
				}
				if r.Header.Get("Authorization") != "Bearer private-test-key" {
					t.Error("missing bearer authentication")
				}
				w.Header().Set("Content-Type", "application/json")
				if requests == 1 {
					_, _ = w.Write([]byte(`{"data":[{"id":101,"attributes":{"name":"Example.S01.1080p.WEB-DL-GRP","tmdb_id":42}},{"id":102,"attributes":{"name":"Other.Work.1080p.WEB-DL-GRP","tmdb_id":99}}],"links":{"next":"?tmdb=42&categories[]=` + wantCategory + `&page=2"}}`))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":103,"attributes":{"name":"Example.S01E02.720p.WEB-DL-GRP","tmdb_id":42}}],"links":{"next":null}}`))
				}
			}))
			defer server.Close()
			profile := Profile()
			profile.BaseURL = server.URL
			definition := unit3d.NewWithProfile(profile)
			registry := trackers.NewRegistry()
			if err := registry.Register(definition); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ACM": {APIKey: "private-test-key"}}}}
			logger := &acmRecordingLogger{}
			adapter := dupe.NewAdapter(definition, "ACM", cfg, server.Client(), logger, registry)
			result := adapter.Search(t.Context(), api.DuplicateSubject{
				Identity:    api.ExternalIdentity{TMDBID: 42, Category: category},
				ReleaseName: "Example.S01E02.1080p.WEB-DL-GRP",
				SeasonInt:   1,
				EpisodeInt:  2,
			})
			if result.Disposition() != dupe.DispositionResolved {
				t.Fatalf("search = %v: %v", result.Disposition(), result.Cause())
			}
			if len(result.Entries()) != 2 || result.SearchEvidence().WrongWorkCount != 1 || result.SearchEvidence().Pages != 2 || !result.SearchEvidence().Complete {
				t.Fatalf("entries = %+v, evidence = %+v", result.Entries(), result.SearchEvidence())
			}
			logs := strings.Join(logger.messages, "\n")
			if !strings.Contains(logs, `"tmdb":["42"]`) {
				t.Error("TRACE missing ACM query parameter")
			}
			for _, secret := range []string{"private-test-key", server.URL} {
				if strings.Contains(logs, secret) {
					t.Error("diagnostics exposed authentication or endpoint")
				}
			}
		})
	}
}

type acmRecordingLogger struct {
	api.NopLogger
	messages []string
}

func (l *acmRecordingLogger) Tracef(format string, args ...any) {
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}
func (l *acmRecordingLogger) Debugf(format string, args ...any) {
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}
