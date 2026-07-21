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
	failures, err := New(tracker).evaluateRules(context.Background(), meta, api.NopLogger{})
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

func TestEvaluateRulesCountryRestrictionsAreStrict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tracker string
		country string
		rule    string
	}{
		{
name: "AZ redirect",
 tracker: "AZ",
 country: "US",
 rule: "country_redirect",
},
		{
name: "CZ block",
 tracker: "CZ",
 country: "AQ",
 rule: "country_block",
},
		{
name: "PHD block",
 tracker: "PHD",
 country: "AQ",
 rule: "country_block",
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
					if failure.Disposition != api.RuleDispositionStrict {
						t.Fatalf("%s disposition = %q, want strict", test.rule, failure.Disposition)
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
