// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

// ProgrammeLanguageStatus assesses eligibility against the existing MediaInfo-selected
// resource for non-disc packs. Canonical facts, answer keys and coverage remain unchanged.
func ProgrammeLanguageStatus(subject api.TrackerValidationSubject) api.MetadataEvidenceStatus {
	facts := subject.LanguageFacts
	if facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial || facts.TrackCoverageComplete ||
		IsFullDiscUpload(subject.DiscType, subject.Type) || len(subject.FileList) < 2 ||
		subject.VideoPath == "" || !slices.Contains(subject.FileList, subject.VideoPath) || facts.PrimaryAudioTrackID == "" {
		return facts.ProgrammeStatus
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
		return facts.ProgrammeStatus
	}
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && (track.ResourceID != primary.ResourceID ||
			track.ManifestFingerprint != primary.ManifestFingerprint || track.DiscID != "" || track.PlaylistID != "") {
			return facts.ProgrammeStatus
		}
	}
	return mediafacts.InspectedProgrammeStatus(facts)
}

func programmeCoverageGuidance(subject api.TrackerValidationSubject) []api.RuleFailure {
	if ProgrammeLanguageStatus(subject) == subject.LanguageFacts.ProgrammeStatus {
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
