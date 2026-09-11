// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns DreadVault's waivable horror and adult-content checks.
// Both checks inspect provider genres and TMDB keywords.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-dvl-policy-v1", Check: checkContent}
}

func checkContent(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 2)
	ruleSubject := unit3d.ValidationRuleSubject(meta)
	terms := append(trackers.RuleGenres(ruleSubject), unit3d.RuleKeywords(ruleSubject)...)
	// Horror may appear within a compound genre or keyword.
	if !containsTermSubstring(terms, "horror") {
		failures = append(failures, trackers.NewRuleFailure("genre", "Only horror content is allowed at DVL.", api.RuleDispositionWaivable))
	}
	// Match whole terms so compound keywords such as "adult animation" remain allowed.
	adultKeywords := []string{"xxx", "porn", "adult", "hentai", "softcore"}
	if unit3d.ContainsRuleValue(terms, adultKeywords) {
		failures = append(failures, trackers.NewRuleFailure("block_adult", "Porn/XXX is not allowed at DVL.", api.RuleDispositionWaivable))
	}
	return failures, nil
}

func containsTermSubstring(values []string, target string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), target) {
			return true
		}
	}
	return false
}
