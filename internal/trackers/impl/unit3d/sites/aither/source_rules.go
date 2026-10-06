// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	for _, track := range additionalMainAudio(subject.LanguageFacts) {
		original := slices.ContainsFunc(
			track.Languages,
			func(language string) bool { return slices.Contains(subject.LanguageFacts.OriginalLanguages, language) },
		)
		outcome := trackers.LanguageStaffException
		reason := "additional main audio " + track.ID + " is outside the ordinary original-plus-English programme set"
		if original {
			outcome = trackers.LanguageUnresolved
			reason = "additional original-language main audio " + track.ID + " requires source confirmation that this is a music video and the audio remains original and non-transcoded"
			switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "music_video_audio_"+track.ID)] {
			case "original_music_video":
				if track.ID != "" {
					continue
				}
			case "not_original_music_video":
				outcome = trackers.LanguageStaffException
			}
		}
		failures = append(failures, trackers.LanguageRuleFailure(subject, "additional_main_audio", reason, outcome))
	}
	failures = append(failures, compatibilityFailures(subject)...)
	if track, ok := multilingualProgrammeTrack(subject.LanguageFacts); ok && multilingualMarker(subject, track) == "" {
		failures = append(failures, trackers.LanguageRuleFailure(subject, "multilingual_balance",
			"source review must establish whether programme track "+track.ID+" is evenly split or has a predominant language", trackers.LanguageUnresolved))
	}
	return failures
}

// additionalMainAudio counts programme options within each inspected resource;
// another episode's original track is not an additional mix of this episode.
func additionalMainAudio(facts api.LanguageFacts) []api.MediaTrackFacts {
	seen := map[string]map[string]bool{}
	var additional []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
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
	var candidates []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role != api.AudioRoleCompatibility && track.ID != "" &&
			strings.Contains(strings.ToLower(track.Codec), "truehd") && track.ResourceID == compatibility.ResourceID &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(compatibility.Languages, language) }) {
			candidates = append(candidates, track)
		}
	}
	return candidates
}

// Compatibility associations are source evidence, never inferred merely from
// matching language or track counts; embedded cores do not satisfy AITHER.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
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
		if track.Role != api.AudioRoleCompatibility {
			continue
		}
		identified := track.ID != "" && len(track.Languages) > 0 && !slices.ContainsFunc(track.Languages, func(language string) bool {
			code := languageutil.NormalizeLanguageCode(language)
			return code == "" || code == "und" || code == "mul"
		})
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
		validCodec := slices.ContainsFunc([]string{"DD", "AC-3", "DD+", "E-AC-3"}, func(codec string) bool { return strings.EqualFold(codec, track.Codec) })
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
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]
		if answer != "" && slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == answer }) {
			if validCodec && identified {
				confirmed[answer] = true
			} else {
				pending[answer] = true
			}
		} else {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"compatibility_mix",
					"source association with its TrueHD mix is unresolved for compatibility track "+track.ID,
					trackers.LanguageUnresolved,
				),
			)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
		}
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || !strings.Contains(strings.ToLower(track.Codec), "truehd") ||
			confirmed[track.ID] {
			continue
		}
		outcome := trackers.LanguageProhibited
		if track.ID == "" || pending[track.ID] || subject.LanguageFacts.AudioStatus != api.MetadataEvidenceStatusComplete {
			outcome = trackers.LanguageUnresolved
		}
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"compatibility_missing",
				"TrueHD mix "+track.ID+" requires its own standalone DD or DD+ compatibility audio",
				outcome,
			),
		)
	}
	return failures
}
