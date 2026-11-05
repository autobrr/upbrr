// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCandidateSearchPreservesLongListAndBillingEvidence(t *testing.T) {
	var detailCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/tv" {
			if r.URL.Query().Get("query") != "Example Series" || r.URL.Query().Get("first_air_date_year") != "2025" {
				t.Error("search request lost title/year")
			}
			items := make([]map[string]any, 9)
			for i := range items {
				items[i] = map[string]any{
					"id":             i + 1,
					"name":           fmt.Sprintf("Example %d", i+1),
					"first_air_date": "2025-01-01",
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
			return
		}
		detailCalls.Add(1)
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/tv/"))
		if id == 9 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if id == 8 {
			id = 99
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":               id,
			"episode_run_time": []int{42},
			"credits": map[string]any{
				"cast": []map[string]any{{"name": "Later", "order": 5}, {"name": "Unknown"}, {"name": "Lead", "order": 0}, {"name": "Second", "order": 1}},
				"crew": []map[string]any{{"name": "Example Director", "job": "Director"}},
			},
		})
	}))
	defer server.Close()
	client := NewClient(server.Client(), nil, "synthetic")
	client.baseURL = server.URL
	got, err := client.SearchCandidates(t.Context(), "Example Series", 2025, api.CanonicalCategoryTV)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 9 || detailCalls.Load() != 9 {
		t.Fatalf("candidates=%d details=%d", len(got), detailCalls.Load())
	}
	if strings.Join(got[0].Cast, ",") != "Lead,Second,Later" || got[0].RuntimeMinutes != 42 || got[0].RuntimeKind != "episode" {
		t.Fatalf("untrustworthy card: %+v", got[0])
	}
	for _, candidate := range got[7:] {
		if candidate.DetailsStatus != api.MetadataEvidenceStatusPartial || len(candidate.Cast) != 0 || candidate.RuntimeMinutes != 0 {
			t.Fatalf("failed details contaminated candidate: %+v", candidate)
		}
	}
}
