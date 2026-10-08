// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func sourceLanguageFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	if needsSubtitleAcknowledgement(subject.LanguageFacts) {
		failure := trackers.NewEvidenceRuleFailure(
			"language_subtitles",
			"English subtitles were not identified locally. HHD requires matching English subtitles locally or through its subtitle manager, even with an English dub; acknowledge the actual track status before proceeding.",
			api.RuleDispositionWaivable,
			subject.LanguageFacts.SubtitleStatus,
		)
		failure.EvidenceFingerprint = api.WorkflowFingerprint(fmt.Sprintf("%x", sha256.Sum256([]byte(trackers.LanguageQuestionKey(subject, "subtitles")))))
		failure.DebugBypass = true
		failures = append(failures, failure)
	}
	if !webSourceVideo(subject) {
		failure := trackers.LanguageRuleFailure(
			subject,
			"source_disc_audio",
			"retain original mixes, commentary and unique isolated scores/music from primary source discs; this is mandatory for personal releases, and equivalent material from additional sources is recommended. Source retention has not been verified",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
	}
	return failures
}

func needsSubtitleAcknowledgement(facts api.LanguageFacts) bool {
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
// explicit input only for otherwise ambiguous mix identity; provenance is advisory.
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
		candidates := compatibilityMixes(subject, track)
		if track.Role != api.AudioRoleCompatibility &&
			(!trackers.StandaloneDolbyAudio(track) || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" })) {
			continue
		}
		mix := compatibilityMix(subject, track, candidates)
		if track.Role != api.AudioRoleCompatibility && mix == "not_compatibility" {
			continue
		}
		failure := trackers.LanguageRuleFailure(
			subject,
			"compatibility_source",
			"compatibility track "+track.ID+" must not separately duplicate an embedded core; web-source exceptions require untouched HLS audio. Source provenance has not been verified",
			trackers.LanguageAdvisory,
		)
		failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		failures = append(failures, failure)
		if track.Codec != "" && !ac3Compatibility(track) && nonWebSourceVideo(subject) {
			add("compatibility_format", "track "+track.ID+" requires industry-standard AC-3 for non-web video", trackers.LanguageProhibited)
		}
		if track.ID == "" || track.Codec == "" || len(trackers.KnownCompatibilityLanguages(track.Languages)) == 0 {
			incompleteCompatibility = true
			add("compatibility_evidence", "compatibility track identity, codec or language needs review: "+track.ID, trackers.LanguageUnresolved)
			continue
		}
		if mix == "" || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.ID == mix && candidate.Codec != "" }) {
			add("compatibility_mix", "source association with an identified mix is unresolved for compatibility track "+track.ID, trackers.LanguageUnresolved)
			for _, candidate := range candidates {
				pending[candidate.ID] = true
			}
			continue
		}
		counts[mix]++
		if ac3Compatibility(track) && !track.EmbeddedCompatibility && !strings.Contains(strings.ToLower(track.Title), "embedded core") {
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

// compatibilityMixes keeps possible parents in the same media resource. Only
// resolved TrueHD companions are excluded as parents; current manual uncertainty
// or rejection remains authoritative. Explicit compatibility keeps HHD's WEB exceptions.
func compatibilityMixes(subject api.TrackerValidationSubject, compatibility api.MediaTrackFacts) []api.MediaTrackFacts {
	facts := subject.LanguageFacts
	candidates := trackers.CompatibilityAudioMixes(facts, compatibility, compatibility.Role != api.AudioRoleCompatibility)
	return slices.DeleteFunc(candidates, func(candidate api.MediaTrackFacts) bool {
		if !trackers.StandaloneDolbyAudio(candidate) || len(trackers.KnownCompatibilityLanguages(candidate.Languages)) == 0 {
			return false
		}
		parents := trackers.CompatibilityAudioMixes(facts, candidate, true)
		mix := compatibilityMix(subject, candidate, parents)
		return mix != "" && slices.ContainsFunc(parents, func(parent api.MediaTrackFacts) bool {
			return parent.ID == mix && parent.Codec != ""
		})
	})
}

// compatibilityMix retains current manual answers, including explicit uncertainty.
func compatibilityMix(subject api.TrackerValidationSubject, track api.MediaTrackFacts, candidates []api.MediaTrackFacts) string {
	if answer, present := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)]; present {
		return answer
	}
	return trackers.AutomaticCompatibilityMix(track, candidates)
}
