// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMinimumContentAgeRuleIsStrict(t *testing.T) {
	t.Parallel()

	ruleSet := Profile().Rules
	if ruleSet == nil || ruleSet.Check == nil {
		t.Fatal("RTF minimum content age rule is not registered")
	}
	failures, err := ruleSet.Check(context.Background(), api.RuleSubject{
		Release: api.ReleaseInfo{Year: 9999},
	}, nil)
	if err != nil {
		t.Fatalf("check RTF rules: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("RTF age failures = %#v, want one", failures)
	}
	failure := failures[0]
	if failure.Rule != "minimum_content_age" || failure.Reason != minimumContentAgeReason || failure.Disposition != api.RuleDispositionStrict {
		t.Fatalf("RTF age failure = %#v, want strict minimum_content_age", failure)
	}
}

func TestMinimumContentAgeBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	if minimumContentAgeViolation(api.RuleSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{ReleaseDate: "2016-07-21"}},
	}, now) {
		t.Fatal("content on the age boundary was rejected")
	}
	if !minimumContentAgeViolation(api.RuleSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{ReleaseDate: "2016-07-22"}},
	}, now) {
		t.Fatal("content newer than the age boundary was allowed")
	}
}
