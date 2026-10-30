// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"slices"
	"sort"

	"github.com/autobrr/upbrr/internal/languageutil"

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
		resourceFailures := presentationLanguageFailures(part, resource)
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
		switch {
		case facts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory:
			add(
				"default_audio",
				"resource "+resource+": primary audio default flags cannot be resolved from contradictory track evidence",
				trackers.LanguageAdvisory,
			)
			failures[len(failures)-1].EvidenceStatus = api.MetadataEvidenceStatusContradictory
		case !subject.Anime && defaults > 1 || defaults == 0 && unknownDefaults == 0 && facts.TrackCoverageComplete:
			failure := trackers.NewEvidenceRuleFailure(
				"language_default_audio",
				fmt.Sprintf("resource %s: %d primary audio tracks are marked default; set exactly one default, or at least one for anime", resource, defaults),
				api.RuleDispositionWaivable,
				api.MetadataEvidenceStatusComplete,
			)
			bindPresentationWarning(&failure, subject, resource)
			failures = append(failures, failure)
		case unknownDefaults > 0 && (!subject.Anime || defaults == 0) || defaults == 0 && !facts.TrackCoverageComplete:
			add("default_audio", "resource "+resource+": primary audio default flags are unresolved", trackers.LanguageAdvisory)
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

// presentationLanguageFailures keeps unverified presentation advice passive and
// warns only about observed ordering/default flags, without asserting trump eligibility.
func presentationLanguageFailures(subject api.TrackerValidationSubject, resource string) []api.RuleFailure {
	failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{
		OriginalFirst:   trackers.LanguageAdvisory,
		OriginalDefault: trackers.LanguageAdvisory,
		SubtitleDefault: trackers.LanguageAdvisory,
	})
	for i := range failures {
		failure := &failures[i]
		// Missing canonical language facts still require correction when no known
		// English coverage makes those facts irrelevant to LST acceptance.
		canonicalEvidence := failure.Rule == "language_evidence" || failure.Rule == "language_original_evidence"
		englishCoverage := (subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory && languageutil.ContainsLanguage(subject.LanguageFacts.ProgrammeLanguages, "English")) ||
			(subject.LanguageFacts.SubtitleStatus != api.MetadataEvidenceStatusContradictory && languageutil.ContainsLanguage(subject.LanguageFacts.SubtitleLanguages, "English"))
		if !canonicalEvidence || englishCoverage {
			failure.Disposition = api.RuleDispositionAdvisory
		}
		reason := ""
		switch failure.Rule {
		case "language_original_order":
			if subject.LanguageFacts.HasOriginalAudio() {
				reason = "original programme audio is not first; place an original programme track first"
			}
		case "language_original_default":
			if subject.LanguageFacts.HasOriginalAudio() {
				reason = "no original programme audio track is marked default; set an original programme track as default"
			}
		case "language_subtitle_default":
			if slices.ContainsFunc(subject.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
				return track.Kind == api.MediaTrackSubtitle && languageutil.ContainsLanguage(track.Languages, "English")
			}) {
				reason = "no English subtitle track is marked default for foreign content; set the appropriate English subtitle default flag"
			}
		}
		if reason != "" && failure.EvidenceStatus == api.MetadataEvidenceStatusComplete &&
			subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory && subject.LanguageFacts.SubtitleStatus != api.MetadataEvidenceStatusContradictory {
			failure.Disposition = api.RuleDispositionWaivable
			failure.Reason = reason
			bindPresentationWarning(failure, subject, resource)
		} else if reason == "" && slices.Contains([]string{"language_original_order", "language_original_default", "language_subtitle_default"}, failure.Rule) {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failure.Reason = "resource " + resource + ": " + failure.Reason
	}
	return failures
}

func bindPresentationWarning(failure *api.RuleFailure, subject api.TrackerValidationSubject, resource string) {
	failure.DebugBypass = true
	failure.EvidenceFingerprint, _ = api.CanonicalWorkflowFingerprint(struct {
		Identity api.ExternalIdentity
		Resource string
		Prepared string
		Anime    bool
		Facts    api.LanguageFacts
		Rule     string
	}{
		Identity: subject.Identity,
		Resource: resource,
		Prepared: subject.PreparedResourceFingerprint,
		Anime:    subject.Anime,
		Facts:    subject.LanguageFacts,
		Rule:     failure.Rule,
	})
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
	return facts.OriginalLanguagesKnown && !languageutil.ContainsLanguage(facts.OriginalLanguages, "English") &&
		!languageutil.ContainsLanguage(facts.OriginalLanguages, "ZXX") &&
		(facts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory || !languageutil.ContainsLanguage(facts.ProgrammeLanguages, "English")) &&
		(facts.SubtitleStatus == api.MetadataEvidenceStatusContradictory || !languageutil.ContainsLanguage(facts.SubtitleLanguages, "English"))
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
