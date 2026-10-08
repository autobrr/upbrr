// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// compatibilityMixes returns selectable mixes and reports matching tracks whose identity is missing.
func compatibilityMixes(facts api.LanguageFacts, compatibility api.MediaTrackFacts) ([]api.MediaTrackFacts, bool) {
	var candidates []api.MediaTrackFacts
	unidentified := false
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role != api.AudioRoleCompatibility &&
			strings.Contains(strings.ToLower(track.Codec), "truehd") && track.ResourceID == compatibility.ResourceID &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(compatibility.Languages, language) }) {
			if track.ID == "" {
				unidentified = true
			} else {
				candidates = append(candidates, track)
			}
		}
	}
	return candidates, unidentified
}

// compatibilityFailures preserves unambiguous matches and requires source review
// for multiple possible TrueHD mixes. ULCX has no additional codec or count limit.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.LanguageFacts
	var failures []api.RuleFailure
	confirmed := map[string]bool{}
	unknownCodec := slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool { return track.Kind == api.MediaTrackAudio && track.Codec == "" })
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Role != api.AudioRoleCompatibility {
			continue
		}
		identified := track.ID != "" && len(track.Languages) > 0 && !slices.ContainsFunc(track.Languages, func(language string) bool {
			code := languageutil.NormalizeLanguageCode(language)
			return code == "" || code == "und" || code == "mul"
		})
		if !identified {
			add("compatibility_evidence", "compatibility track identity and language require review", trackers.LanguageUnresolved)
		}
		candidates, unidentifiedMix := compatibilityMixes(facts, track)
		answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]
		if len(candidates) == 1 && !unidentifiedMix {
			answer = candidates[0].ID
		}
		if answer != "" && slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == answer }) {
			if identified {
				confirmed[answer] = true
			}
			continue
		}
		outcome := trackers.LanguageUnresolved
		reason := "source association with its TrueHD mix is unresolved for compatibility track " + track.ID
		if len(candidates) == 0 && identified && !unidentifiedMix && !unknownCodec && facts.AudioStatus == api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageProhibited
			reason = "compatibility track " + track.ID + " has no matching-language TrueHD mix in its media resource; compatibility for other codecs is prohibited"
		}
		add("compatibility_mix", reason, outcome)
	}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || !strings.Contains(strings.ToLower(track.Codec), "truehd") ||
			confirmed[track.ID] {
			continue
		}
		// An absent or unresolved association cannot establish per-mix coverage.
		add("compatibility_missing", "TrueHD mix "+track.ID+" requires its own compatibility audio", trackers.LanguageUnresolved)
	}
	return failures
}
