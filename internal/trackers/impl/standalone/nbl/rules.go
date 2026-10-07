// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// rules declares NBL's TV-only requirement.
func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireTVOnly: true,
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{EnglishSubtitles: "without_english", MissingSubtitles: trackers.LanguageProhibited}
}

// fullDiscLanguageFailures preserves NBL's existing waivable disc requirement
// independently of the programme-language policy, which excludes full discs.
func fullDiscLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	allowed, reason := trackers.EvaluateLanguageRule(trackers.RuleSubjectFromValidation(subject), &trackers.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonBDMV: true,
	})
	if allowed {
		return nil
	}
	return []api.RuleFailure{trackers.NewRuleFailure("language_rule", reason, api.RuleDispositionWaivable)}
}
