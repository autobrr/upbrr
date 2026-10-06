// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package lume

import (
	"cmp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains LUME's independent media and adult-content requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
	}
}

func personalRecommendationOutcome(subject api.TrackerValidationSubject) trackers.LanguageOutcome {
	if !subject.PersonalRelease {
		return trackers.LanguageAdvisory
	}
	switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "language_personal_exemption")] {
	case "previously_uploaded":
		return trackers.LanguageAdvisory
	case "trash_tier":
		if !subject.Anime {
			return trackers.LanguageAdvisory
		}
	case "anime_tier":
		if subject.Anime {
			return trackers.LanguageAdvisory
		}
	case "none":
		return trackers.LanguageProhibited
	}
	return trackers.LanguageUnresolved
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	recommendation := personalRecommendationOutcome(subject)
	policy := trackers.LanguagePolicy{
		OriginalAudio:    trackers.LanguageProhibited,
		EnglishSubtitles: "spoken",
		MissingSubtitles: trackers.LanguageProhibited,
	}
	if facts.AudioAbsent {
		policy.OriginalAudio = ""
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	if !slices.Contains(facts.OriginalLanguages, "ZXX") && slices.Contains(facts.SubtitleLanguages, "English") &&
		!slices.Contains(facts.FullSubtitleLanguages, "English") {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"subtitle_coverage",
				"complete English subtitles corresponding to original dialogue are not established",
				trackers.LanguageUnresolved,
			),
		)
	}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio && track.Kind != api.MediaTrackSubtitle {
			continue
		}
		if len(track.Languages) == 0 || slices.ContainsFunc(track.Languages, func(value string) bool {
			code := languageutil.NormalizeLanguageCode(value)
			return code == "" || code == "und" || code == "mul"
		}) {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"track_language",
					"correct language assignment is unresolved for track "+track.ID,
					trackers.LanguageUnresolved,
				),
			)
		}
	}
	failures = append(failures, trackOrderingFailures(subject, recommendation)...)
	failures = append(failures, trackMetadataFailures(subject, recommendation)...)
	return failures
}

// trackOrderingFailures assesses measured container order without treating secondary
// tracks as normal dubs. Original alternate mixes belong with original audio.
func trackOrderingFailures(subject api.TrackerValidationSubject, outcome trackers.LanguageOutcome) []api.RuleFailure {
	facts := subject.LanguageFacts
	if facts.AudioAbsent || !facts.OriginalLanguagesKnown || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		return nil
	}
	audioTracks, orderKnown := orderedAudioTracks(facts.Tracks)
	var failures []api.RuleFailure
	if !orderKnown {
		failures = unresolvedTrackOrder(subject, outcome)
	}
	otherAudioSeen, dubGroupEnded := false, false
	lastDub := ""
	dubTracks := 0
	multilingualDub := false
	for _, track := range audioTracks {
		original := (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) })
		if original && otherAudioSeen {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "original_order", "original programme track "+track.ID+" should precede other audio tracks", outcome),
			)
		}
		otherAudioSeen = otherAudioSeen || !original
		if original || track.Role != api.AudioRoleProgramme {
			dubGroupEnded = dubTracks > 0
			continue
		}
		dubTracks++
		dubOrder := dubGroupEnded
		if len(track.Languages) != 1 {
			// One multilingual stream cannot be reordered into separate tracks.
			multilingualDub = true
			lastDub = ""
		} else {
			language := track.Languages[0]
			dubOrder = dubOrder || (lastDub != "" && compareDubLanguages(lastDub, language) > 0)
			lastDub = language
		}
		if dubOrder {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"dub_order",
					"normal dub track "+track.ID+" should be grouped with normal dubs, English first and remaining languages alphabetically ordered",
					outcome,
				),
			)
		}
	}
	if multilingualDub && dubTracks > 1 {
		if outcome != trackers.LanguageAdvisory {
			outcome = trackers.LanguageUnresolved
		}
		failures = append(
			failures,
			trackers.LanguageRuleFailure(subject, "dub_order_evidence", "normal dub ordering is unresolved for a multilingual programme track", outcome),
		)
	}
	return failures
}

// orderedAudioTracks returns the unambiguous measured subset without changing
// canonical facts. The boolean reports whether all audio can be ordered; missing
// tracks cannot erase violations already established by the measured subset.
func orderedAudioTracks(tracks []api.MediaTrackFacts) ([]api.MediaTrackFacts, bool) {
	var audio []api.MediaTrackFacts
	orders := make(map[int]int)
	for _, track := range tracks {
		if track.Kind == api.MediaTrackAudio {
			audio = append(audio, track)
			if track.StreamOrderKnown && track.StreamOrder >= 0 {
				orders[track.StreamOrder]++
			}
		}
	}
	if len(audio) < 2 {
		return audio, true
	}
	count := len(audio)
	audio = slices.DeleteFunc(audio, func(track api.MediaTrackFacts) bool {
		return !track.StreamOrderKnown || track.StreamOrder < 0 || orders[track.StreamOrder] != 1
	})
	slices.SortFunc(audio, func(left, right api.MediaTrackFacts) int { return cmp.Compare(left.StreamOrder, right.StreamOrder) })
	return audio, len(audio) == count
}

func unresolvedTrackOrder(subject api.TrackerValidationSubject, outcome trackers.LanguageOutcome) []api.RuleFailure {
	if outcome == trackers.LanguageProhibited {
		switch subject.QuestionnaireAnswers[trackOrderQuestionKey(subject)] {
		case "ordered":
			return nil
		case "out_of_order":
			return []api.RuleFailure{trackers.LanguageRuleFailure(
				subject,
				"track_order",
				"reviewed audio order does not put originals first or group normal dubs with English first and remaining languages alphabetically",
				outcome,
			)}
		}
	}
	if outcome != trackers.LanguageAdvisory {
		outcome = trackers.LanguageUnresolved
	}
	failure := trackers.LanguageRuleFailure(
		subject,
		"track_order_evidence",
		"audio ordering is unresolved because container stream order is missing or ambiguous",
		outcome,
	)
	failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
	return []api.RuleFailure{failure}
}

func compareDubLanguages(left, right string) int {
	if left == "English" && right != "English" {
		return -1
	}
	if right == "English" && left != "English" {
		return 1
	}
	return strings.Compare(left, right)
}

// trackMetadataFailures reports measured omissions independently of the personal
// review: an attestation cannot clear contradictory inspected metadata. Default
// suitability and semantic title accuracy require review, not invented flag rules.
func trackMetadataFailures(subject api.TrackerValidationSubject, outcome trackers.LanguageOutcome) []api.RuleFailure {
	var failures []api.RuleFailure
	hasTracks := false
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio && track.Kind != api.MediaTrackSubtitle {
			continue
		}
		hasTracks = true
		if strings.TrimSpace(track.Title) == "" {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "track_titles", "a descriptive title should be assigned to track "+track.ID, outcome),
			)
		}
		flagMismatch := false
		if track.Kind == api.MediaTrackSubtitle {
			flagMismatch = slices.ContainsFunc(track.Languages, func(language string) bool {
				_, coverage := languageutil.SubtitleLanguageParts(language)
				return strings.EqualFold(coverage, "Forced") && !track.Forced || strings.EqualFold(coverage, "Full") && track.Forced
			})
		}
		if flagMismatch {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"track_flags",
					"the forced flag should match the established subtitle coverage of track "+track.ID,
					outcome,
				),
			)
		}
	}
	if !hasTracks || outcome == trackers.LanguageAdvisory {
		return failures
	}
	answer := subject.QuestionnaireAnswers[trackMetadataQuestionKey(subject)]
	if outcome == trackers.LanguageProhibited && answer == "appropriate" {
		return failures
	}
	reason := "appropriateness of track flags, default status and descriptive titles needs personal-release review"
	switch {
	case outcome == trackers.LanguageUnresolved:
		reason = "personal-release recommendation exemption needs review"
	case answer == "inappropriate":
		reason = "appropriate flags, default status and descriptive titles are required for this personal release"
	default:
		outcome = trackers.LanguageUnresolved
	}
	return append(failures, trackers.LanguageRuleFailure(subject, "track_metadata", reason, outcome))
}
