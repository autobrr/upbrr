// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func sourceQuestionKey(subject api.TrackerValidationSubject, key string) string {
	subject = primaryTechnicalSubject(subject)
	fingerprint, _ := api.CanonicalWorkflowFingerprint(struct {
		Resolution, Encoder, Codec string
		Season, Episode            int
		Pack                       bool
		HasEncodeSettings          bool
		Features                   []api.ReleaseFeature
		Assessments                api.ReleaseAssessments
		Files                      api.MediaFileFacts
		Provider                   api.SourceScopedMetadata
	}{subject.Release.Resolution, subject.VideoEncoder, subject.VideoCodec, subject.SeasonInt, subject.EpisodeInt, subject.TVPack, subject.HasEncodeSettings, subject.ReleaseFeatures, subject.Assessments, subject.MediaFileFacts, subject.ProviderMetadata})
	return trackers.LanguageQuestionKey(subject, key+"_"+string(fingerprint))
}

func needsModernEncodeReview(subject api.TrackerValidationSubject) bool {
	if !strings.EqualFold(subject.Type, "ENCODE") || trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return false
	}
	if !singleLegacyContainerCandidate(subject) {
		return true
	}
	return slices.ContainsFunc(subject.MediaFileFacts.Files, func(file api.MediaFileFact) bool {
		return !singleLegacyContainerCandidate(containerFileSubject(subject, file))
	})
}

func normalizedLSTSource(value string) string {
	return strings.NewReplacer("-", "", " ", "", "_", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
}

func lstBelowHD(resolution string) bool {
	return slices.Contains([]string{"480i", "480p", "576i", "576p"}, strings.ToLower(strings.TrimSpace(resolution)))
}

func lstEncoderIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "hi10p x264" {
		return "x264"
	}
	return value
}

func lstKnownEncoder(value string) bool {
	return slices.Contains([]string{"x264", "x265", "av1"}, lstEncoderIdentity(value))
}

func lstUnknownEncoder(value string) bool {
	return slices.Contains([]string{"", "h.264", "h264", "avc", "h.265", "h265", "hevc"}, strings.ToLower(strings.TrimSpace(value)))
}

func endedLSTSeries(subject api.TrackerValidationSubject) bool {
	metadata := subject.ProviderMetadata
	if !metadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return false
	}
	return metadata.TVDB != nil && strings.EqualFold(metadata.TVDB.Status, "Ended") ||
		metadata.TVmaze != nil && strings.EqualFold(metadata.TVmaze.Status, "Ended")
}

func sourceValidationFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	subject = primaryTechnicalSubject(subject)
	var failures []api.RuleFailure
	add := func(rule, reason string, status api.MetadataEvidenceStatus, disposition api.RuleDisposition) {
		key := "lst_" + rule
		if disposition == api.RuleDispositionAdvisory && rule != "stream_processing" {
			key = "guidance_" + key
		}
		failure := trackers.NewEvidenceRuleFailure(key, reason, disposition, status)
		failure.DebugBypass = true
		failure.EvidenceFingerprint = api.WorkflowFingerprint(sourceQuestionKey(subject, rule))
		failures = append(failures, failure)
	}
	if prohibitedLSTSource(subject.Source) {
		add("prohibited_source", "The established source is not permitted", api.MetadataEvidenceStatusComplete, api.RuleDispositionStrict)
	}
	for _, file := range subject.MediaFileFacts.Files {
		if prohibitedLSTSource(file.Source) {
			add(
				"resource_prohibited_source",
				"Resource "+file.ResourceID+": the established source is not permitted",
				api.MetadataEvidenceStatusComplete,
				api.RuleDispositionStrict,
			)
		}
	}
	if subject.Identity.Category == api.CanonicalCategoryTV && subject.EpisodeInt > 0 && !subject.TVPack {
		if endedLSTSeries(subject) {
			add(
				"finished_season_episode",
				"The series has ended; upload a season pack instead of an individual episode",
				api.MetadataEvidenceStatusComplete,
				api.RuleDispositionWaivable,
			)
		} else {
			add(
				"episode_season",
				"Check the selected season's airing schedule before uploading an individual episode; current evidence does not establish that the season has finished",
				api.MetadataEvidenceStatusPartial,
				api.RuleDispositionAdvisory,
			)
		}
	}
	if normalizedLSTSource(subject.Source) == "WORKPRINT" ||
		slices.ContainsFunc(subject.MediaFileFacts.Files, func(file api.MediaFileFact) bool { return normalizedLSTSource(file.Source) == "WORKPRINT" }) {
		add(
			"workprint_retail",
			"Check source records for an official retail workprint; inspected media does not establish retail provenance",
			api.MetadataEvidenceStatusPartial,
			api.RuleDispositionAdvisory,
		)
	}
	if needsModernEncodeReview(subject) {
		add(
			"encode_source",
			"Check untouched retail source provenance and actual black-bar cropping against the source. Final dimensions and naming do not establish either; a below-HD output does not establish that the original source was below HD",
			api.MetadataEvidenceStatusPartial,
			api.RuleDispositionAdvisory,
		)
		if len(subject.MediaFileFacts.Files) == 0 {
			add(
				"encode_rate_control",
				"Check the encoder settings for CRF or two-pass rate control. A generic VBR label does not establish either mode",
				api.MetadataEvidenceStatusPartial,
				api.RuleDispositionAdvisory,
			)
		}
	}
	inspectEncode := func(prefix, resource, resolution, encoder, codec string, settingsKnown, hasSettings bool) {
		if subject.MediaFileFacts.TechnicalStatus == api.MetadataEvidenceStatusContradictory {
			add(
				prefix+"encode_evidence",
				resource+"Technical evidence is contradictory; inspect the resource before assessing encoder, resolution or settings",
				api.MetadataEvidenceStatusContradictory,
				api.RuleDispositionAdvisory,
			)
			return
		}
		resolution, encoder = strings.ToLower(strings.TrimSpace(resolution)), lstEncoderIdentity(encoder)
		if lstUnknownEncoder(encoder) {
			add(
				prefix+"encode_encoder_evidence",
				resource+"Encoder identity is unresolved; the codec or release name cannot establish the writing library",
				api.MetadataEvidenceStatusPartial,
				api.RuleDispositionAdvisory,
			)
		} else if !lstKnownEncoder(encoder) {
			add(
				prefix+"encode_encoder",
				resource+"The inspected encoder "+encoder+" is not supported; use a supported encoder",
				api.MetadataEvidenceStatusComplete,
				api.RuleDispositionWaivable,
			)
		}
		if (lstBelowHD(resolution) || resolution == "720p") &&
			(encoder == "x265" || encoder == "av1" || slices.Contains([]string{"h.265", "hevc", "av1"}, strings.ToLower(codec))) {
			add(
				prefix+"encode_resolution_codec",
				resource+"The inspected encode is 720p or below and requires x264",
				api.MetadataEvidenceStatusComplete,
				api.RuleDispositionWaivable,
			)
		}
		if resolution == "" {
			add(prefix+"encode_resolution_evidence", resource+"Encode resolution is unresolved", api.MetadataEvidenceStatusPartial, api.RuleDispositionAdvisory)
		}
		if settingsKnown && !hasSettings {
			add(
				prefix+"encode_settings",
				resource+"Encode settings are missing from MediaInfo; retain the encoder settings in the output",
				api.MetadataEvidenceStatusComplete,
				api.RuleDispositionWaivable,
			)
		} else if !settingsKnown {
			add(
				prefix+"encode_settings_evidence",
				resource+"Inspect MediaInfo to establish the encode settings; absence has not been verified",
				api.MetadataEvidenceStatusPartial,
				api.RuleDispositionAdvisory,
			)
		}
	}
	if strings.EqualFold(subject.Type, "ENCODE") && !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		if !singleLegacyContainerCandidate(subject) {
			hasSettings := subject.HasEncodeSettings || subject.Assessments.MediaInfoEncodeSettings == api.EncodeSettingsStatusPresent
			inspectEncode(
				"",
				"",
				subject.Release.Resolution,
				subject.VideoEncoder,
				subject.VideoCodec,
				hasSettings || subject.Assessments.MediaInfoEncodeSettings == api.EncodeSettingsStatusMissing,
				hasSettings,
			)
		}
		for _, file := range subject.MediaFileFacts.Files {
			if !singleLegacyContainerCandidate(containerFileSubject(subject, file)) {
				inspectEncode(
					"resource_",
					"Resource "+file.ResourceID+": ",
					file.Resolution,
					file.VideoEncoder,
					file.VideoCodec,
					file.EncodeSettingsKnown || file.HasEncodeSettings,
					file.HasEncodeSettings,
				)
				if subject.MediaFileFacts.TechnicalStatus == api.MetadataEvidenceStatusContradictory {
					continue
				}
				switch file.RateControl {
				case api.VideoRateControlCRF, api.VideoRateControlTwoPassVBR:
				case api.VideoRateControlMultiPassVBR:
					add(
						"resource_encode_rate_control",
						"Resource "+file.ResourceID+": settings report a final statistics-reading VBR pass, but do not establish the total pass count",
						api.MetadataEvidenceStatusPartial,
						api.RuleDispositionAdvisory,
					)
				case api.VideoRateControlABR, api.VideoRateControlCBR, api.VideoRateControlCQP:
					add(
						"resource_encode_rate_control",
						"Resource "+file.ResourceID+": encoder settings explicitly report "+string(file.RateControl)+" rate control; use CRF or two-pass VBR",
						api.MetadataEvidenceStatusComplete,
						api.RuleDispositionWaivable,
					)
				default:
					add(
						"resource_encode_rate_control",
						"Resource "+file.ResourceID+": rate control is not established by the inspected encoder settings; a generic VBR label does not prove CRF or two-pass encoding",
						api.MetadataEvidenceStatusPartial,
						api.RuleDispositionAdvisory,
					)
				}
			}
		}
	}
	add(
		"stream_processing",
		"Review source records for every prepared resource, including video, audio, subtitles and HDR layers. Distinguish studio/distributor processing already present at retail from user-generated/enhanced streams, and check for improper splicing.",
		api.MetadataEvidenceStatusPartial,
		api.RuleDispositionAdvisory,
	)
	return failures
}

func prohibitedLSTSource(value string) bool {
	return slices.Contains(
		[]string{
			"SCREENER",
			"SCR",
			"DVDSCR",
			"BDSCR",
			"DDC",
			"TELESYNC",
			"TS",
			"CAM",
			"HDCAM",
			"TELECINE",
			"TC",
			"DCP",
			"THEATRERIP",
			"THEATERRIP",
			"R5",
			"R5DVD",
			"BOOTLEG",
		},
		normalizedLSTSource(value),
	)
}
