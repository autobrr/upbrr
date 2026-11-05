// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCandidateSearchUsesExactYearAndPrincipalCreditsWithoutRuntimeDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables struct {
				Constraints struct {
					ReleaseDateConstraint struct{ ReleaseDateRange struct{ Start, End string } }
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		rangeValues := request.Variables.Constraints.ReleaseDateConstraint.ReleaseDateRange
		if rangeValues.Start != "2025-01-01" || rangeValues.End != "2025-12-31" {
			t.Error("year silently broadened")
		}
		_, _ = w.Write([]byte(`{"data":{"advancedTitleSearch":{"edges":[{"node":{"title":{"id":"tt0123456","titleText":{"text":"Example"},"titleType":{"text":"Movie"},"principalCredits":[{"category":{"text":"Stars"},"credits":[{"name":{"id":"nm001","nameText":{"text":"Featured"}}}]},{"category":{"text":"Directors"},"credits":[{"name":{"id":"nm002","nameText":{"text":"Director"}}}]}]}}},{"node":{"title":{"id":"tt0123457","titleType":{"text":"TV Series"}}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(server.Client(), nil)
	client.baseURL = server.URL
	got, err := client.SearchCandidates(t.Context(), "Example", 2025, api.CanonicalCategoryMovie)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 123456 || got[0].RuntimeMinutes != 0 || got[0].Overview != "" || got[0].CastSource != "principal" || len(got[0].Cast) != 1 || got[0].Cast[0] != "Featured" {
		t.Fatalf("candidate lost absent-field/principal semantics: %+v", got)
	}
}
