// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn/xxx is not allowed at BHD.",
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{
		OriginalPrimary:         true,
		OriginalAudio:           trackers.LanguageProhibited,
		ExtraDubs:               trackers.LanguageProhibited,
		CompatibilityOnlyTrueHD: true,
		CompatibilityCodecs:     []string{"DD", "AC-3"},
	}
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return failures
	}
	facts := subject.LanguageFacts
	// Source comparisons remain partial-evidence guidance; saved answers cannot
	// establish source history or semantic equivalence between programme mixes.
	addGuidance := func(key, reason string) {
		failure := trackers.LanguageRuleFailure(subject, key, reason, trackers.LanguageAdvisory)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	addGuidance(
		"existing_release",
		"adding audio, subtitles or chapters to an existing release requires staff approval; source history is not established by track metadata",
	)
	if needsProgrammeMixReview(facts) {
		addGuidance(
			"redundant_original",
			"compare same-language programme tracks with their source mixes; an alternate-mix label or different codec establishes neither distinct content nor a duplicate main mix",
		)
	}
	animated := subject.Anime || slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool { return strings.EqualFold(genre, "Animation") })
	if animated && facts.OriginalLanguagesKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX") &&
		facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && facts.HasOriginalAudio() && !facts.HasEnglishDub() {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject,
			"animated_dual_audio",
			"original plus English dual audio is preferred over original-only audio for animated content",
			trackers.LanguageAdvisory,
		))
	}
	if strings.EqualFold(subject.Type, "REMUX") {
		addGuidance(
			"source_extras",
			"available source subtitles, commentary and chapters should accompany the remux; source availability and retention are not established",
		)
		if needsForeignDialogueReview(facts) {
			addGuidance(
				"foreign_dialogue_subtitles",
				"English subtitles for foreign dialogue should be separate and forced when needed; track metadata does not establish dialogue coverage",
			)
		}
	}
	return failures
}

// needsProgrammeMixReview detects same-language tracks within one resource. Role labels and
// codec differences do not establish whether two tracks preserve distinct mixes.
func needsProgrammeMixReview(facts api.LanguageFacts) bool {
	seen := make(map[string][]string)
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) ||
			trackers.AutomaticDolbyCompatibilityMix(facts, track) != "" {
			continue
		}
		for _, language := range track.Languages {
			if slices.Contains(seen[track.ResourceID], language) {
				return true
			}
		}
		seen[track.ResourceID] = append(seen[track.ResourceID], track.Languages...)
	}
	return false
}

func needsForeignDialogueReview(facts api.LanguageFacts) bool {
	return !facts.AudioAbsent && (facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || !slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}
