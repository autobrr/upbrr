// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains LST's independent MediaInfo requirement.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{RequireValidMISetting: true} }

// encodeAudioGuidance recommends sample-depth reduction only for an inspected
// primary programme track. Unknown evidence never creates a recommendation.
func encodeAudioGuidance(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) || !strings.EqualFold(subject.Type, "ENCODE") ||
		!strings.EqualFold(subject.Release.Resolution, "1080p") || subject.LanguageFacts.PrimaryAudioTrackID == "" {
		return nil
	}
	var primary *api.MediaTrackFacts
	for i := range subject.LanguageFacts.Tracks {
		track := &subject.LanguageFacts.Tracks[i]
		if track.ID != subject.LanguageFacts.PrimaryAudioTrackID {
			continue
		}
		if primary != nil {
			return nil
		}
		primary = track
	}
	if primary == nil || primary.Kind != api.MediaTrackAudio || primary.Role != api.AudioRoleProgramme || primary.Commentary ||
		!primary.StreamOrderKnown || primary.StreamOrder < 0 || !strings.EqualFold(primary.Codec, "FLAC") || primary.BitDepth != 24 ||
		!primary.ChannelsKnown || (primary.Channels != 1 && primary.Channels != 2) {
		return nil
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ID == primary.ID || track.ResourceID != primary.ResourceID ||
			track.Commentary || track.Role == api.AudioRoleCommentary || track.Role == api.AudioRoleCompatibility {
			continue
		}
		if !track.StreamOrderKnown || track.StreamOrder <= primary.StreamOrder {
			return nil
		}
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"guidance_lst_flac_bit_depth",
		"For a 1080p encode, LST recommends dithering the primary 24-bit mono/stereo FLAC audio to 16-bit.",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusComplete,
	)}
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	recommendation := trackers.LanguageAdvisory
	if subject.PersonalRelease {
		recommendation = trackers.LanguageProhibited
	}
	policy := trackers.LanguagePolicy{
		OriginalFirst:   trackers.LanguageAdvisory,
		OriginalDefault: recommendation,
		SubtitleDefault: recommendation,
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	facts := subject.LanguageFacts
	defaults, unknownDefaults := 0, 0
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix {
			if !track.DefaultKnown {
				unknownDefaults++
			} else if track.Default {
				defaults++
			}
		}
	}
	if !subject.Anime && defaults > 1 || defaults == 0 && unknownDefaults == 0 {
		add("default_audio", "exactly one primary audio track must be default; anime may have multiple defaults", trackers.LanguageProhibited)
	} else if unknownDefaults > 0 && (!subject.Anime || defaults == 0) {
		add("default_audio", "primary audio default flags must establish the required default count", trackers.LanguageUnresolved)
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
	foreign := facts.OriginalLanguagesKnown && !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX")
	if foreign && !slices.Contains(facts.ProgrammeLanguages, "English") && !slices.Contains(facts.SubtitleLanguages, "English") {
		evidence := subject.TitleSearchEvidence
		outcome := trackers.LanguageUnresolved
		reason := "missing English subtitles; complete title-wide search is required"

		if evidence.HasOtherTorrent(subject.Identity) {
			outcome = trackers.LanguageProhibited
			reason = "missing English subtitles; other torrents exist for this title"
		} else if facts.SubtitleStatus == api.MetadataEvidenceStatusComplete && evidence.Current(subject.Identity) {
			outcome = trackers.LanguageTrumpable
			reason = "missing English subtitles"
		}

		add("subtitles_search", reason, outcome)
		failures[len(failures)-1].EvidenceFingerprint = evidence.ResultFingerprint
	}
	return failures
}
