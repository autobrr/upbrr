// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func sourceLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	if needsSubtitleManagerReview(subject.LanguageFacts) {
		outcome := trackers.LanguageUnresolved
		reason := "English subtitle coverage must be established locally or through HHD's subtitle manager, even with an English dub"
		switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "english_subtitle_manager")] {
		case "available":
			outcome = ""
		case "missing":
			if subject.LanguageFacts.SubtitleStatus == api.MetadataEvidenceStatusComplete {
				outcome = trackers.LanguageProhibited
				reason = "English subtitles are missing locally and from HHD's subtitle manager"
			}
		}
		if outcome != "" {
			failures = append(failures, trackers.LanguageRuleFailure(subject, "subtitles", reason, outcome))
		}
	}
	if webSourceVideo(subject) {
		return failures
	}
	answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "source_disc_audio")]
	if answer == "retained" || answer == "no_disc_source" && !discSourceVideo(subject) {
		return failures
	}
	outcome := trackers.LanguageAdvisory
	reason := "source review must establish whether original mixes, commentary and unique isolated scores/music from the primary source discs were retained; equivalent material from additional sources is recommended"
	if subject.PersonalRelease {
		outcome = trackers.LanguageUnresolved
	}
	if answer == "incomplete" {
		reason = "original mixes, commentary or unique isolated scores/music from the primary source discs were omitted; retain these tracks, with equivalent material from additional sources recommended"
		if subject.PersonalRelease {
			outcome = trackers.LanguageProhibited
		}
	}
	failure := trackers.LanguageRuleFailure(subject, "source_disc_audio", reason, outcome)
	if answer != "incomplete" {
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
	}
	return append(failures, failure)
}

func needsSubtitleManagerReview(facts api.LanguageFacts) bool {
	return facts.OriginalLanguagesKnown && len(facts.OriginalLanguages) > 0 &&
		!slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX") &&
		!slices.Contains(facts.SubtitleLanguages, "English")
}

func webSourceVideo(subject api.TrackerValidationSubject) bool {
	switch strings.ToUpper(strings.TrimSpace(subject.Source)) {
	case "WEB", "WEB-DL", "WEBDL", "WEBRIP", "WEB-RIP":
		return true
	case "":
		return strings.EqualFold(subject.Type, "WEBDL") || strings.EqualFold(subject.Type, "WEBRIP")
	default:
		return false
	}
}

func discSourceVideo(subject api.TrackerValidationSubject) bool {
	switch strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(subject.Source), "-", "")) {
	case "BLURAY", "BLU RAY", "BLURAY 3D", "BD", "BDMV", "DVD", "PAL DVD", "NTSC DVD", "HDDVD", "HD DVD":
		return true
	default:
		return strings.EqualFold(subject.Type, "REMUX") || strings.EqualFold(subject.Type, "DVDRIP") || trackers.IsDiscType(subject.DiscType)
	}
}

// nonWebSourceVideo requires positive source evidence; an unknown encode may
// still qualify for the WEB-only untouched-HLS compatibility exception.
func nonWebSourceVideo(subject api.TrackerValidationSubject) bool {
	if discSourceVideo(subject) {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(subject.Source)) {
	case "HDTV", "UHDTV":
		return true
	case "":
		return strings.EqualFold(subject.Type, "HDTV")
	default:
		return false
	}
}

// compatibilityFailures uses measured tracks for codec/count constraints and
// source attestations only for provenance and otherwise ambiguous mix identity.
func compatibilityFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.LanguageFacts
	var failures []api.RuleFailure
	add := func(key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(subject, key, reason, outcome))
	}
	counts, ac3Counts := map[string]int{}, map[string]int{}
	pending := map[string]bool{}
	incompleteCompatibility := false
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Codec == "" {
			add("compatibility_evidence", "audio codec is unresolved for track "+track.ID, trackers.LanguageUnresolved)
		}
		if track.Role != api.AudioRoleCompatibility {
			continue
		}
		source := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_source_"+track.ID)]
		switch source {
		case "duplicated_core":
			add("compatibility_core", "separately duplicated embedded core: "+track.ID, trackers.LanguageProhibited)
		case "not_duplicated_core":
		case "untouched_hls":
			if !webSourceVideo(subject) {
				add("compatibility_source", "untouched HLS provenance is permitted only for web-source video: "+track.ID, trackers.LanguageUnresolved)
			}
		default:
			add(
				"compatibility_source",
				"source provenance and absence of a separately duplicated embedded core require review for track "+track.ID,
				trackers.LanguageUnresolved,
			)
		}
		if track.Codec != "" && !ac3Compatibility(track) && (source != "untouched_hls" || !webSourceVideo(subject)) {
			outcome := trackers.LanguageProhibited
			if !nonWebSourceVideo(subject) && source != "duplicated_core" && (!webSourceVideo(subject) || source != "not_duplicated_core") {
				outcome = trackers.LanguageUnresolved
			}
			add(
				"compatibility_format",
				"track "+track.ID+" requires industry-standard AC-3 or source-confirmed untouched HLS compatibility audio for web video",
				outcome,
			)
		}
		if track.ID == "" || track.Codec == "" || len(track.Languages) == 0 ||
			slices.ContainsFunc(track.Languages, func(language string) bool {
				code := languageutil.NormalizeLanguageCode(language)
				return code == "" || code == "und" || code == "mul"
			}) {
			incompleteCompatibility = true
			add("compatibility_evidence", "compatibility track identity, codec or language needs review: "+track.ID, trackers.LanguageUnresolved)
			continue
		}
		candidates := compatibilityMixes(facts, track)
		mix := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]
		if len(candidates) == 1 {
			mix = candidates[0].ID
		}
		if mix == "" || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == mix }) {
			add("compatibility_mix", "source association with an identified mix is unresolved for compatibility track "+track.ID, trackers.LanguageUnresolved)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
			continue
		}
		counts[mix]++
		if ac3Compatibility(track) {
			ac3Counts[mix]++
		}
	}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility {
			continue
		}
		if counts[track.ID] > 1 {
			add("compatibility_count", "at most one compatibility track is permitted for mix "+track.ID, trackers.LanguageProhibited)
		}
		if strings.Contains(strings.ToLower(track.Codec), "truehd") && ac3Counts[track.ID] == 0 {
			outcome := trackers.LanguageProhibited
			if incompleteCompatibility || pending[track.ID] || facts.AudioStatus != api.MetadataEvidenceStatusComplete {
				outcome = trackers.LanguageUnresolved
			}
			add(
				"compatibility_missing",
				"TrueHD mix "+track.ID+" requires standalone industry-standard AC-3; embedded cores and HLS substitutes do not satisfy this requirement",
				outcome,
			)
		}
	}
	return failures
}

func ac3Compatibility(track api.MediaTrackFacts) bool {
	return strings.EqualFold(track.Codec, "DD") || strings.EqualFold(track.Codec, "AC-3")
}

// compatibilityMixes keeps association within an inspected media resource when
// resource identities are available; multiple matches require source review.
func compatibilityMixes(facts api.LanguageFacts, compatibility api.MediaTrackFacts) []api.MediaTrackFacts {
	var candidates []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role == api.AudioRoleCompatibility || track.ID == "" {
			continue
		}
		if compatibility.ResourceID != track.ResourceID {
			continue
		}
		if slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(compatibility.Languages, language) }) {
			candidates = append(candidates, track)
		}
	}
	return candidates
}
