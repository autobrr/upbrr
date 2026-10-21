// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"sort"

	"github.com/autobrr/upbrr/internal/mediafacts"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains LST's independent MediaInfo requirement.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{RequireValidMISetting: true} }

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	recommendation := trackers.LanguageAdvisory
	if subject.PersonalRelease {
		recommendation = trackers.LanguageProhibited
	}
	policy := trackers.LanguagePolicy{
		OriginalFirst:   recommendation,
		OriginalDefault: recommendation,
		SubtitleDefault: recommendation,
	}
	var failures []api.RuleFailure
	resources := map[string][]api.MediaTrackFacts{}
	for _, track := range subject.LanguageFacts.Tracks {
		resources[track.ResourceID] = append(resources[track.ResourceID], track)
	}
	for _, file := range subject.MediaFileFacts.Files {
		if _, ok := resources[file.ResourceID]; !ok {
			resources[file.ResourceID] = nil
		}
	}
	if len(resources) == 0 {
		resources[""] = nil
	}
	keys := make([]string, 0, len(resources))
	for resource := range resources {
		keys = append(keys, resource)
	}
	sort.Strings(keys)
	for _, resource := range keys {
		part := subject
		part.LanguageFacts = resourceLanguageFacts(subject, resource, resources[resource])
		resourceFailures := trackers.EvaluateLanguagePolicy(part, policy)
		if !subject.PersonalRelease &&
			((part.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory && slices.Contains(part.LanguageFacts.ProgrammeLanguages, "English")) || (part.LanguageFacts.SubtitleStatus != api.MetadataEvidenceStatusContradictory && slices.Contains(part.LanguageFacts.SubtitleLanguages, "English"))) {
			for i := range resourceFailures {
				if resourceFailures[i].Rule == "language_original_evidence" || resourceFailures[i].Rule == "language_evidence" {
					resourceFailures[i].Disposition = api.RuleDispositionAdvisory
				}
			}
		}
		for i := range resourceFailures {
			resourceFailures[i].Reason = "resource " + resource + ": " + resourceFailures[i].Reason
		}
		failures = append(failures, resourceFailures...)
	}
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}

	for _, resource := range keys {
		facts := resourceLanguageFacts(subject, resource, resources[resource])
		if facts.AudioAbsent {
			continue
		}
		defaults, unknownDefaults := 0, 0
		if !slices.ContainsFunc(resources[resource], func(t api.MediaTrackFacts) bool {
			return t.Kind == api.MediaTrackAudio && (t.Role == api.AudioRoleProgramme || t.Role == api.AudioRoleAlternateMix)
		}) {
			unknownDefaults++
		}
		for _, track := range resources[resource] {
			if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
				continue
			}
			if !track.DefaultKnown {
				unknownDefaults++
			} else if track.Default {
				defaults++
			}
		}
		if !subject.Anime && defaults > 1 || defaults == 0 && unknownDefaults == 0 {
			add("default_audio", "resource "+resource+": exactly one primary audio track should be default; anime may have multiple defaults", recommendation)
		} else if unknownDefaults > 0 && (!subject.Anime || defaults == 0) {
			outcome := trackers.LanguageAdvisory
			if subject.PersonalRelease {
				outcome = trackers.LanguageUnresolved
			}
			add("default_audio", "resource "+resource+": primary audio default flags are unresolved", outcome)
			failures[len(failures)-1].EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
	}
	for _, defect := range audioDefects(subject) {
		add(
			defect.key,
			defect.reason+"; trumpability requires an avoidable drawback and a replacement without losing important material; those conditions have not been verified",
			trackers.LanguageAdvisory,
		)
		failures[len(failures)-1].EvidenceStatus = api.MetadataEvidenceStatusPartial
	}
	failures = append(failures, sourceLanguageFailures(subject)...)
	for _, resource := range keys {
		facts := resourceLanguageFacts(subject, resource, resources[resource])
		if resourceNeedsEnglishSubtitles(facts) {
			evidence := subject.TitleSearchEvidence
			outcome := trackers.LanguageUnresolved
			reason := "missing English subtitles; complete title-wide search is required"

			switch {
			case facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete || facts.SubtitleStatus != api.MetadataEvidenceStatusComplete:
				reason = "English programme audio and subtitle coverage remain unresolved"
			case evidence.HasEnglishSubtitleTorrent(subject.Identity):
				outcome = trackers.LanguageProhibited
				reason = "missing English subtitles; another torrent has verified English subtitles"
			case facts.SubtitleStatus == api.MetadataEvidenceStatusComplete && evidence.NoEnglishSubtitleTorrent(subject.Identity):
				outcome = trackers.LanguageTrumpable
				reason = "missing English subtitles"
			}

			add("subtitles_search", "resource "+resource+": "+reason, outcome)
			failures[len(failures)-1].EvidenceFingerprint = evidence.ResultFingerprint
		}
	}
	return failures
}

// resourceLanguageFacts keeps resource eligibility separate from canonical answer identity.
func resourceLanguageFacts(subject api.TrackerValidationSubject, resource string, tracks []api.MediaTrackFacts) api.LanguageFacts {
	canonical := subject.LanguageFacts
	media := api.MediaFacts{Tracks: tracks, TrackCoverageComplete: canonical.TrackCoverageComplete}
	var file *api.MediaFileFact
	for i := range subject.MediaFileFacts.Files {
		if subject.MediaFileFacts.Files[i].ResourceID == resource {
			file = &subject.MediaFileFacts.Files[i]
			break
		}
	}
	if file != nil {
		media.TrackCoverageComplete = file.AudioStatus == api.MetadataEvidenceStatusComplete || file.SubtitleStatus == api.MetadataEvidenceStatusComplete
		media.AudioAbsent = file.AudioStatus == api.MetadataEvidenceStatusComplete && len(file.AudioLanguages) == 0 &&
			!slices.ContainsFunc(tracks, func(t api.MediaTrackFacts) bool { return t.Kind == api.MediaTrackAudio })
	} else if len(tracks) == 0 {
		media.TrackCoverageComplete = false
	}
	for _, track := range tracks {
		if track.Kind == api.MediaTrackSubtitle {
			media.SubtitleLanguages = append(media.SubtitleLanguages, track.Languages...)
		}
	}
	if file != nil {
		media.SubtitleLanguages = slices.Clone(file.SubtitleLanguages)
	}
	singleResource := true
	for _, track := range canonical.Tracks {
		if track.ResourceID != resource {
			singleResource = false
		}
	}
	for _, candidate := range subject.MediaFileFacts.Files {
		if candidate.ResourceID != resource {
			singleResource = false
		}
	}
	if singleResource && len(tracks) > 0 {
		if file == nil && !slices.ContainsFunc(tracks, func(t api.MediaTrackFacts) bool { return t.Kind == api.MediaTrackSubtitle }) {
			media.SubtitleLanguages = slices.Clone(canonical.SubtitleLanguages)
		}
		media.HardcodedSubtitleLanguages = slices.Clone(subject.HardcodedSubtitleLanguages)
		media.HardcodedSubtitleCoverage = slices.Clone(subject.HardcodedSubtitleCoverage)
	}
	facts := mediafacts.ResolveLanguages(media)
	if file == nil && len(canonical.SubtitleLanguages) == 0 {
		facts.SubtitleLanguages = nil
		facts.FullSubtitleLanguages = nil
	}
	facts.OriginalLanguages = slices.Clone(canonical.OriginalLanguages)
	facts.OriginalLanguagesKnown = canonical.OriginalLanguagesKnown
	if canonical.ProgrammeStatus == api.MetadataEvidenceStatusContradictory {
		facts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
	} else if len(canonical.ProgrammeLanguages) == 0 && canonical.ProgrammeStatus == api.MetadataEvidenceStatusPartial && !facts.AudioAbsent {
		facts.ProgrammeLanguages = nil
		facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	}
	if file != nil {
		if file.AudioStatus == api.MetadataEvidenceStatusContradictory {
			facts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		}
		if file.AudioStatus != api.MetadataEvidenceStatusComplete && facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
			facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		} else if facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
			facts.ProgrammeStatus = mediafacts.InspectedProgrammeStatus(facts)
			if facts.AudioAbsent {
				facts.ProgrammeStatus = api.MetadataEvidenceStatusComplete
			}
		}
		facts.SubtitleStatus = file.SubtitleStatus
	}
	if canonical.SubtitleStatus == api.MetadataEvidenceStatusContradictory {
		facts.SubtitleStatus = api.MetadataEvidenceStatusContradictory
	}
	facts.AudioStatus = facts.ProgrammeStatus
	if !facts.OriginalLanguagesKnown && facts.AudioStatus != api.MetadataEvidenceStatusContradictory {
		facts.AudioStatus = api.MetadataEvidenceStatusPartial
	}
	return facts
}

func resourceNeedsEnglishSubtitles(facts api.LanguageFacts) bool {
	return facts.OriginalLanguagesKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX") &&
		(facts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory || !slices.Contains(facts.ProgrammeLanguages, "English")) && (facts.SubtitleStatus == api.MetadataEvidenceStatusContradictory || !slices.Contains(facts.SubtitleLanguages, "English"))
}

func resourceTitleSearchRequired(subject api.TrackerValidationSubject) bool {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return false
	}
	resources := map[string][]api.MediaTrackFacts{}
	for _, track := range subject.LanguageFacts.Tracks {
		resources[track.ResourceID] = append(resources[track.ResourceID], track)
	}
	for _, file := range subject.MediaFileFacts.Files {
		if _, ok := resources[file.ResourceID]; !ok {
			resources[file.ResourceID] = nil
		}
	}
	for resource, tracks := range resources {
		facts := resourceLanguageFacts(subject, resource, tracks)
		if resourceNeedsEnglishSubtitles(facts) && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete &&
			facts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
			return true
		}
	}
	return false
}
