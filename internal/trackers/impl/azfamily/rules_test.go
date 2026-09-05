// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func evaluateSiteRules(t *testing.T, tracker string, meta api.RuleSubject) []api.RuleFailure {
	t.Helper()
	failures, err := New(tracker).evaluateRules(
		context.Background(),
		api.NewTrackerValidationSubjectFromRuleSubject(meta, tracker),
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("evaluate %s rules: %v", tracker, err)
	}
	return failures
}

func TestEvaluateRulesAZRedirectsEnglishTerritories(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "AZ", api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginCountry: []string{"US"}},
		},
	})
	if len(failures) == 0 {
		t.Fatal("expected AZ rule failure")
	}
}

func TestEvaluateRulesCZRejectsAsianContent(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "CZ", api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginCountry: []string{"JP"}},
		},
	})
	if len(failures) == 0 {
		t.Fatal("expected CZ rule failure")
	}
}

func TestEvaluateRulesCountryRestrictionsUseCorrectDisposition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		tracker         string
		country         string
		rule            string
		wantDisposition api.RuleDisposition
	}{
		{
			name:            "AZ redirect",
			tracker:         "AZ",
			country:         "US",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "CZ redirect",
			tracker:         "CZ",
			country:         "JP",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "PHD redirect",
			tracker:         "PHD",
			country:         "JP",
			rule:            "country_redirect",
			wantDisposition: api.RuleDispositionWaivable,
		},
		{
			name:            "CZ block",
			tracker:         "CZ",
			country:         "AQ",
			rule:            "country_block",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name:            "PHD block",
			tracker:         "PHD",
			country:         "AQ",
			rule:            "country_block",
			wantDisposition: api.RuleDispositionStrict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failures := evaluateSiteRules(t, test.tracker, api.RuleSubject{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{OriginCountry: []string{test.country}},
				},
			})
			for _, failure := range failures {
				if failure.Rule == test.rule {
					if failure.Disposition != test.wantDisposition {
						t.Fatalf("%s disposition = %q, want %q", test.rule, failure.Disposition, test.wantDisposition)
					}
					return
				}
			}
			t.Fatalf("missing %s failure: %#v", test.rule, failures)
		})
	}
}

func TestEvaluateRulesPHDRejectsSDAndBlockedGroup(t *testing.T) {
	t.Parallel()

	failures := evaluateSiteRules(t, "PHD", api.RuleSubject{
		Identity:  api.ExternalIdentity{Category: "MOVIE"},
		Release:   api.ReleaseInfo{Resolution: "480p"},
		Container: "avi",
		Tag:       "-RARBG",
	})
	if len(failures) < 2 {
		t.Fatalf("expected multiple PHD failures, got %v", failures)
	}
}
