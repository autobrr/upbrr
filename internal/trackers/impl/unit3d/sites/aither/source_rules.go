// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"maps"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	associationReview := additionalMainAudioAssociationReview(subject)
	for _, track := range additionalMainAudio(subject) {
		original := slices.ContainsFunc(
			track.Languages,
			func(language string) bool { return slices.Contains(subject.LanguageFacts.OriginalLanguages, language) },
		)
		outcome := trackers.LanguageStaffException
		reason := "additional main audio " + track.ID + " is outside the ordinary original-plus-English programme set"
		if original {
			outcome = trackers.LanguageAdvisory
			reason = "additional original-language main audio " + track.ID + " is permitted for music videos in original, non-transcoded form; source history has not been verified"
		}
		failure := trackers.LanguageRuleFailure(subject, "additional_main_audio", reason, outcome)
		failure.QuestionnaireInput = !original && len(associationReview) > 0
		if original {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	failures = append(failures, compatibilityFailures(subject)...)
	if track, ok := multilingualProgrammeTrack(subject); ok && multilingualMarker(subject, track) == "" {
		failure := trackers.LanguageRuleFailure(subject, "multilingual_balance",
			"source review must establish whether programme track "+track.ID+" is evenly split or has a predominant language", trackers.LanguageUnresolved)
		failure.QuestionnaireInput = true
		failures = append(failures, failure)
	}
	return failures
}

// additionalMainAudioAssociationReview proves that revising known companion
// choices removes every strict extra-main gate, independently of stream order.
func additionalMainAudioAssociationReview(subject api.TrackerValidationSubject) map[string]bool {
	hasStrictExtra := func(candidate api.TrackerValidationSubject) bool {
		return slices.ContainsFunc(additionalMainAudio(candidate), func(track api.MediaTrackFacts) bool {
			return !slices.ContainsFunc(track.Languages, func(language string) bool {
				return slices.Contains(candidate.LanguageFacts.OriginalLanguages, language)
			})
		})
	}
	if !hasStrictExtra(subject) {
		return nil
	}
	reviewed := subject
	reviewed.QuestionnaireAnswers = maps.Clone(subject.QuestionnaireAnswers)
	keys := map[string]bool{}
	for _, track := range subject.LanguageFacts.Tracks {
		key := trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)
		if subject.QuestionnaireAnswers[key] != "not_compatibility" || !compatibilityCandidate(subject.LanguageFacts, track) ||
			len(trackers.KnownCompatibilityLanguages(track.Languages)) == 0 {
			continue
		}
		for _, mix := range compatibilityMixes(subject.LanguageFacts, track) {
			if mix.ID != "" && mix.Codec != "" {
				reviewed.QuestionnaireAnswers[key] = mix.ID
				keys[key] = true
				break
			}
		}
	}
	if len(keys) == 0 || hasStrictExtra(reviewed) {
		return nil
	}
	return keys
}

// additionalMainAudio counts programme options within each inspected resource;
// another episode's original track is not an additional mix of this episode.
// Potential companion streams are excluded unless current review rejects them.
func additionalMainAudio(subject api.TrackerValidationSubject) []api.MediaTrackFacts {
	facts := subject.LanguageFacts
	seen := map[string]map[string]bool{}
	var additional []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		if compatibilityCandidate(facts, track) &&
			subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)] != "not_compatibility" {
			continue
		}
		if seen[track.ResourceID] == nil {
			seen[track.ResourceID] = map[string]bool{}
		}
		duplicate := false
		for _, language := range track.Languages {
			if slices.Contains(facts.OriginalLanguages, language) {
				language = "original"
			}
			if seen[track.ResourceID][language] {
				duplicate = true
			}
		}
		for _, language := range track.Languages {
			if slices.Contains(facts.OriginalLanguages, language) {
				language = "original"
			}
			seen[track.ResourceID][language] = true
		}
		if duplicate {
			additional = append(additional, track)
		}
	}
	return additional
}

func compatibilityMixes(facts api.LanguageFacts, compatibility api.MediaTrackFacts) []api.MediaTrackFacts {
	return trackers.CompatibilityAudioMixes(facts, compatibility, true)
}

// compatibilityCandidate exposes inspected standalone streams for source review
// without changing their canonical roles or assuming a mix association.
func compatibilityCandidate(facts api.LanguageFacts, track api.MediaTrackFacts) bool {
	if track.Kind != api.MediaTrackAudio || track.EmbeddedCompatibility || strings.Contains(strings.ToLower(track.Title), "embedded core") {
		return false
	}
	if track.Role == api.AudioRoleCompatibility {
		return true
	}
	return trackers.StandaloneDolbyAudio(track) &&
		slices.ContainsFunc(compatibilityMixes(facts, track), func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" })
}

// resolveCompatibilityMix preserves current manual choices, including blank or
// unresolved answers. Without one, a standalone companion can match exactly one
// same-resource TrueHD mix with the same known languages and no distinct role.
// The boolean reports inference so the questionnaire can keep manual choices editable.
func resolveCompatibilityMix(subject api.TrackerValidationSubject, track api.MediaTrackFacts) (string, bool) {
	if answer, exists := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]; exists {
		return answer, false
	}
	answer := trackers.AutomaticDolbyCompatibilityMix(subject.LanguageFacts, track)
	return answer, answer != ""
}

// Compatibility associations use current review or unambiguous same-language
// standalone companions. Embedded cores cannot satisfy a missing companion.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures, _ := compatibilityAssessment(subject)
	return failures
}

func compatibilityAssessment(subject api.TrackerValidationSubject) ([]api.RuleFailure, bool) {
	var failures []api.RuleFailure
	var companions []trackers.CompatibilityCompanion
	var required []api.MediaTrackFacts
	confirmed, pending := map[string]bool{}, map[string]bool{}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Codec == "" {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_evidence", "audio codec requires review for track "+track.ID, trackers.LanguageUnresolved),
			)
		}
		if !compatibilityCandidate(subject.LanguageFacts, track) {
			continue
		}
		answer, _ := resolveCompatibilityMix(subject, track)
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		if track.ID != "" && len(trackers.KnownCompatibilityLanguages(track.Languages)) > 0 && trackers.DolbyCompatibilityCodec(track.Codec) {
			companion := trackers.CompatibilityCompanion{
				ID:         track.ID,
				ResourceID: track.ResourceID,
				CanCover:   true,
				Optional:   track.Role != api.AudioRoleCompatibility,
			}
			for _, candidate := range candidates {
				if candidate.ID != "" && candidate.Codec != "" {
					companion.Mixes = append(companion.Mixes, candidate.ID)
				}
			}
			companions = append(companions, companion)
		}
		if track.Role != api.AudioRoleCompatibility && answer == "not_compatibility" {
			continue
		}
		identified := track.ID != "" && len(trackers.KnownCompatibilityLanguages(track.Languages)) > 0
		if !identified {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_evidence",
					"compatibility track identity and language require review",
					trackers.LanguageUnresolved,
				),
			)
		}
		validCodec := trackers.DolbyCompatibilityCodec(track.Codec)
		if !validCodec {
			outcome := trackers.LanguageProhibited
			if track.Codec == "" {
				outcome = trackers.LanguageUnresolved
			}
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "compatibility_format", "standalone DD or DD+ is required for compatibility track "+track.ID, outcome),
			)
		}
		if answer != "" &&
			slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == answer && candidate.Codec != "" }) {
			if validCodec && identified {
				confirmed[answer] = true
			} else {
				pending[answer] = true
			}
		} else {
			failure := trackers.LanguageRuleFailure(
				subject,
				"compatibility_mix",
				"source association with its TrueHD mix is unresolved for compatibility track "+track.ID,
				trackers.LanguageUnresolved,
			)
			failure.QuestionnaireInput = slices.ContainsFunc(
				candidates,
				func(candidate api.MediaTrackFacts) bool { return candidate.ID != "" && candidate.Codec != "" },
			)
			failures = append(failures, failure)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || !strings.Contains(strings.ToLower(track.Codec), "truehd") {
			continue
		}
		required = append(required, track)
		if confirmed[track.ID] {
			continue
		}
		outcome := trackers.LanguageProhibited
		if track.ID == "" || pending[track.ID] || subject.LanguageFacts.AudioStatus != api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageUnresolved
		}
		failure := trackers.LanguageRuleFailure(
			subject,
			"compatibility_missing",
			"TrueHD mix "+track.ID+" requires its own standalone DD or DD+ compatibility audio",
			outcome,
		)
		failure.QuestionnaireInput = track.ID != "" && pending[track.ID] && subject.LanguageFacts.AudioStatus == api.MetadataEvidenceStatusComplete
		failures = append(failures, failure)
	}
	review := subject.LanguageFacts.AudioStatus == api.MetadataEvidenceStatusComplete &&
		trackers.ReviewCompatibilityAssignments(failures, required, companions, false)
	return failures, review
}
