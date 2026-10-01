// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DDuplicateSearchRejectsUnresolvedCategoryScope(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		category api.CanonicalCategory
		tmdbID   int
		profile  SiteProfile
	}
	tests := make([]testCase, 0, 10)
	tests = append(tests, []testCase{
		{name: "missing provider ID", category: api.CanonicalCategoryTV},
		{name: "missing category", tmdbID: 123},
		{
			name:     "unsupported category",
			category: "FANRES",
			tmdbID:   123,
		},
		{
			name:     "category override without scope",
			category: api.CanonicalCategoryTV,
			tmdbID:   123,
			profile:  SiteProfile{ResolveCategoryID: func(api.UploadSubject) string { return "3" }},
		},
		{
			name:     "empty scope",
			category: api.CanonicalCategoryTV,
			tmdbID:   123,
			profile:  SiteProfile{CategoryIDs: func(api.CanonicalCategory) []string { return nil }},
		},
	}...)
	for _, invalidID := range []string{"", "0", "-1", " 3 ", "anime"} {
		tests = append(tests, testCase{
			name:     "invalid native ID " + invalidID,
			category: api.CanonicalCategoryTV,
			tmdbID:   123,
			profile:  SiteProfile{CategoryIDs: func(api.CanonicalCategory) []string { return []string{"2", invalidID} }},
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte(`{"data":[],"links":{"next":null}}`))
			}))
			defer server.Close()
			definition := NewWithProfile(Profile{
				Name:    "AITHER",
				BaseURL: server.URL,
				Site:    test.profile,
			})
			registry := trackers.NewRegistry()
			if err := registry.Register(definition); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "test-key"},
			}}}
			adapter := dupe.NewAdapter(definition, "AITHER", cfg, server.Client(), api.NopLogger{}, registry)
			result := adapter.Search(t.Context(), api.DuplicateSubject{
				Identity: api.ExternalIdentity{TMDBID: test.tmdbID, Category: test.category},
			})
			if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata ||
				result.SearchEvidence().EffectiveComplete() || requests.Load() != 0 {
				t.Fatalf("unresolved scope searched: disposition=%v code=%s evidence=%+v requests=%d",
					result.Disposition(), result.Code(), result.SearchEvidence(), requests.Load())
			}
		})
	}
}

func TestUnit3DValidationRejectsUploadCategoryOutsideSearchScope(t *testing.T) {
	t.Parallel()

	definition := NewWithProfile(Profile{
		Name: "SCOPE",
		Site: SiteProfile{
			ResolveCategoryID: func(api.UploadSubject) string { return "4" },
			CategoryIDs:       func(api.CanonicalCategory) []string { return []string{"2", "3"} },
		},
	})
	failures, err := definition.ValidationPolicy().Check(t.Context(), api.TrackerValidationSubject{
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Type:       "WEBDL",
		SeasonInt:  1,
		EpisodeInt: 2,
	}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Rule != "unsupported_category" || failures[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("category outside declared scope was not blocked before duplicate search: %+v", failures)
	}
}
