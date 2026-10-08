// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// languageFailures assesses language findings from finalized evidence.
// Legacy payload choices never authorize a language-rule waiver.
func languageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	if ptpNoProgrammeDialogue(facts) {
		return ptpRemuxLanguageFailures(subject)
	}
	var failures []api.RuleFailure
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	needsOriginal := slices.ContainsFunc(facts.ProgrammeLanguages, func(language string) bool { return language != "English" && language != "ZXX" })
	if (needsOriginal && !facts.OriginalLanguagesKnown) || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		add("evidence", "original/programme language and track-role evidence is unresolved", trackers.LanguageUnresolved)
	} else if len(facts.ProgrammeLanguages) > 0 && !facts.HasOriginalAudio() && !slices.Contains(facts.ProgrammeLanguages, "English") {
		add("non_english_dub", "Non-English Language Dub; a replacement must resolve this defect", trackers.LanguageTrumpable)
	}
	failures = append(failures, ptpRemuxLanguageFailures(subject)...)
	if ptpNeedsTrackPurposeReview(facts) {
		failure := trackers.LanguageRuleFailure(
			subject,
			"redundant_audio",
			"additional programme tracks should preserve distinct necessary mixes/content; track counts and codecs cannot establish redundant mixes or superfluous dubs. Programme tracks: "+ptpProgrammeTrackDetails(
				facts,
			),
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	primary := ptpPrimaryProgrammeLanguage(facts)
	if primary == "" && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && !slices.Contains(facts.SubtitleLanguages, "English") {
		failure := trackers.LanguageRuleFailure(
			subject,
			"primary_evidence",
			"primary programme language is unresolved; check English-subtitle guidance where applicable",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	if primary != "" && primary != "English" && primary != "ZXX" && !slices.Contains(facts.SubtitleLanguages, "English") {
		failure := trackers.LanguageRuleFailure(
			subject,
			"english_subtitles",
			"English subtitles should be available locally or in PTP's subtitle manager; manager availability cannot be established from the upload alone",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	return failures
}

// ptpRemuxLanguageFailures checks measured remux track/default boundaries
// independently of source-comparison guidance about additional programme audio.
func ptpRemuxLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !strings.EqualFold(strings.TrimSpace(subject.Type), "REMUX") {
		return nil
	}
	facts := subject.LanguageFacts
	if facts.AudioAbsent {
		return nil
	}
	var failures []api.RuleFailure
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	primary, primaryKnown := ptpPrimaryProgrammeTrack(facts)
	switch {
	case !facts.TrackCoverageComplete || !primaryKnown:
		add(
			"remux_track_order",
			"the inspected main programme track and complete remux track manifest must be established before ordering can be assessed",
			trackers.LanguageUnresolved,
		)
	case !primary.DefaultKnown:
		add(
			"remux_track_order",
			"default main audio is not established for track "+primary.ID+"; inspect its default flag before assessing remux order",
			trackers.LanguageUnresolved,
		)
	default:
		if !primary.Default {
			add(
				"remux_main_default",
				"the main programme audio is explicitly not flagged default; a compliant replacement must correct its default flag",
				trackers.LanguageTrumpable,
			)
		}
		outOfOrder, complete := ptpRemuxOrderEvidence(facts)
		if !complete {
			switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "remux_track_order")] {
			case "main_first":
			case "out_of_order":
				outOfOrder = true
			default:
				add(
					"remux_track_order_evidence",
					"container ordering must establish that default main audio precedes secondary audio and subtitles",
					trackers.LanguageUnresolved,
				)
			}
		}
		if outOfOrder {
			add(
				"remux_track_order",
				"default main audio does not precede every secondary audio track and subtitle; a compliant replacement must correct the remux order",
				trackers.LanguageTrumpable,
			)
		}
	}
	if subject.Anime && !ptpNoProgrammeDialogue(facts) && facts.TrackCoverageComplete && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete {
		if reason, outcome := ptpAnimeRemuxProgrammeIssue(facts); reason != "" {
			add("anime_remux_programme", reason+". Programme tracks: "+ptpProgrammeTrackDetails(facts), outcome)
		}
	}
	return failures
}

// ptpRemuxOrderEvidence uses measured cross-kind container order only. A known
// inversion remains a defect even when another track's position is unknown.
func ptpRemuxOrderEvidence(facts api.LanguageFacts) (outOfOrder, complete bool) {
	primary, ok := ptpPrimaryProgrammeTrack(facts)
	if !ok {
		return false, false
	}
	complete = true
	seen := map[int]bool{}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio && track.Kind != api.MediaTrackSubtitle {
			continue
		}
		if !track.StreamOrderKnown || track.StreamOrder < 0 || seen[track.StreamOrder] {
			complete = false
		} else {
			seen[track.StreamOrder] = true
		}
		if track.ID != primary.ID && primary.StreamOrderKnown && track.StreamOrderKnown && track.StreamOrder >= 0 && track.StreamOrder < primary.StreamOrder {
			outOfOrder = true
		}
	}
	// No relative order needs establishing when there is only the main track.
	if !slices.ContainsFunc(facts.Tracks, func(track api.MediaTrackFacts) bool {
		return (track.Kind == api.MediaTrackAudio || track.Kind == api.MediaTrackSubtitle) && track.ID != primary.ID
	}) {
		return false, true
	}
	return outOfOrder, complete
}

func ptpRemuxNeedsOrderReview(facts api.LanguageFacts) bool {
	primary, ok := ptpPrimaryProgrammeTrack(facts)
	if !ok || !primary.DefaultKnown || !facts.TrackCoverageComplete {
		return false
	}
	_, complete := ptpRemuxOrderEvidence(facts)
	return !complete
}

func ptpAnimeRemuxProgrammeIssue(facts api.LanguageFacts) (string, trackers.LanguageOutcome) {
	seen := map[string]bool{}
	excess := false
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		if len(track.Languages) != 1 {
			return "anime remux programme-track languages do not establish the one-Japanese/one-English track count", trackers.LanguageUnresolved
		}
		language := languageutil.NormalizeLanguageLabel(track.Languages[0])
		if (language != "Japanese" && language != "English") || seen[language] {
			excess = true
		}
		seen[language] = true
	}
	if excess {
		return "anime remuxes permit at most one Japanese and one English programme track, excluding commentary; a compliant replacement must correct the excess programme tracks", trackers.LanguageTrumpable
	}
	return "", ""
}

func ptpPrimaryProgrammeTrack(facts api.LanguageFacts) (api.MediaTrackFacts, bool) {
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || facts.PrimaryAudioTrackID == "" {
		return api.MediaTrackFacts{}, false
	}
	var primary api.MediaTrackFacts
	count := 0
	for _, track := range facts.Tracks {
		if track.ID == facts.PrimaryAudioTrackID && track.Kind == api.MediaTrackAudio &&
			(track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			primary = track
			count++
		}
	}
	return primary, count == 1
}

func ptpProgrammeTrackDetails(facts api.LanguageFacts) string {
	var details []string
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			details = append(details, strings.TrimSpace(track.ID+" "+track.Title)+" ["+strings.Join(track.Languages, ", ")+"]")
		}
	}
	return strings.Join(details, "; ")
}

func ptpNoProgrammeDialogue(facts api.LanguageFacts) bool {
	return (facts.AudioAbsent && len(facts.ProgrammeLanguages) == 0) ||
		(facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete && slices.Equal(facts.ProgrammeLanguages, []string{"ZXX"}))
}

// ptpPrimaryProgrammeLanguage uses the inspected primary stream, never original
// metadata or an optional English/secondary track elsewhere in the release.
func ptpPrimaryProgrammeLanguage(facts api.LanguageFacts) string {
	track, ok := ptpPrimaryProgrammeTrack(facts)
	if !ok || len(track.Languages) != 1 {
		return ""
	}
	code := languageutil.NormalizeLanguageCode(track.Languages[0])
	if code == "" || code == "und" || code == "mul" {
		return ""
	}
	return languageutil.NormalizeLanguageLabel(track.Languages[0])
}

// ptpNeedsTrackPurposeReview identifies source-comparison guidance. Track counts and
// language differences alone never prove that a mix or dub is redundant.
func ptpNeedsTrackPurposeReview(facts api.LanguageFacts) bool {
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		return false
	}
	seen := map[string]bool{}
	programmeTracks := 0
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		programmeTracks++
		for _, language := range track.Languages {
			if seen[language] {
				return true
			}
			seen[language] = true
		}
	}
	return programmeTracks > 1 && facts.OriginalLanguagesKnown && slices.ContainsFunc(facts.ProgrammeLanguages, func(language string) bool {
		return language != "English" && language != "ZXX" && !slices.Contains(facts.OriginalLanguages, language)
	})
}
