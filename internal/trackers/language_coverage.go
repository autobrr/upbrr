// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

// ProgrammeLanguageStatus assesses eligibility against the existing MediaInfo-selected
// resource for non-disc packs. Canonical facts, answer keys and coverage remain unchanged.
func ProgrammeLanguageStatus(subject api.TrackerValidationSubject) api.MetadataEvidenceStatus {
	facts := subject.LanguageFacts
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial || facts.TrackCoverageComplete ||
		!selectedProgrammeResourceInspected(subject) {
		return facts.ProgrammeStatus
	}
	return mediafacts.InspectedProgrammeStatus(facts)
}

// ProgrammeAudioStatus assesses original/dub eligibility without requiring every
// language within an explicitly original multilingual soundtrack. Canonical
// language identities remain partial for naming, disclosure and subtitle checks.
func ProgrammeAudioStatus(subject api.TrackerValidationSubject) api.MetadataEvidenceStatus {
	status := ProgrammeLanguageStatus(subject)
	facts := subject.LanguageFacts
	if status != api.MetadataEvidenceStatusPartial || len(facts.ProgrammeLanguages) == 0 ||
		!facts.TrackCoverageComplete && !selectedProgrammeResourceInspected(subject) {
		return status
	}
	var languages []string
	multilingual := false
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Role == "" {
			return status
		}
		if track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix {
			continue
		}
		originalMultilingual := confirmedOriginalMultilingualTrack(track)
		if facts.ProgrammeAudioOrigin(track) == "" || len(track.Languages) == 0 ||
			!originalMultilingual && slices.ContainsFunc(track.Languages, func(language string) bool {
				return !languageutil.IsIdentifiedLanguage(language)
			}) {
			return status
		}
		multilingual = multilingual || originalMultilingual
		languages = append(languages, track.Languages...)
	}
	aggregateKeys := languageutil.LanguageMatchKeys(facts.ProgrammeLanguages)
	trackKeys := languageutil.LanguageMatchKeys(languages)
	slices.Sort(aggregateKeys)
	slices.Sort(trackKeys)
	if multilingual && slices.Equal(aggregateKeys, trackKeys) {
		return api.MetadataEvidenceStatusComplete
	}
	return status
}

func confirmedOriginalMultilingualTrack(track api.MediaTrackFacts) bool {
	return track.AudioOrigin == api.TrackAudioOriginal && languageutil.ContainsLanguage(track.Languages, "mul") &&
		!slices.ContainsFunc(track.Languages, func(language string) bool {
			return !languageutil.IsIdentifiedLanguage(language) && !languageutil.SameLanguage(language, "mul")
		})
}

func selectedProgrammeResourceInspected(subject api.TrackerValidationSubject) bool {
	facts := subject.LanguageFacts
	if IsFullDiscUpload(subject.DiscType, subject.Type) || len(subject.FileList) < 2 ||
		subject.VideoPath == "" || !slices.Contains(subject.FileList, subject.VideoPath) || facts.PrimaryAudioTrackID == "" {
		return false
	}
	var primary api.MediaTrackFacts
	primaryCount := 0
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.ID == facts.PrimaryAudioTrackID &&
			(track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) {
			primary = track
			primaryCount++
		}
	}
	if primaryCount != 1 || primary.ResourceID == "" || primary.ManifestFingerprint == "" {
		return false
	}
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.ResourceID != primary.ResourceID ||
			track.ManifestFingerprint != primary.ManifestFingerprint || track.DiscID != "" || track.PlaylistID != "") {
			return false
		}
	}
	return true
}

func programmeCoverageGuidance(subject api.TrackerValidationSubject) []api.RuleFailure {
	if subject.LanguageFacts.TrackCoverageComplete || ProgrammeAudioStatus(subject) == subject.LanguageFacts.ProgrammeStatus {
		return nil
	}
	failure := NewRuleFailure(
		"guidance_programme_pack_coverage",
		"Programme-language evidence comes from the MediaInfo-selected file; other pack files remain uninspected.",
		api.RuleDispositionAdvisory,
	)
	failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
	return []api.RuleFailure{failure}
}
