// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
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
	}{subject.Release.Resolution, subject.VideoEncoder, subject.VideoCodec, subject.SeasonInt, subject.EpisodeInt, subject.TVPack, subject.HasEncodeSettings, subject.ReleaseFeatures, subject.Assessments, subject.MediaFileFacts})
	return trackers.LanguageQuestionKey(subject, key+"_"+string(fingerprint))
}

func sourceReviewQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	subject = primaryTechnicalSubject(subject)
	var fields []api.TrackerQuestionnaireField
	add := func(key, label, help string, options ...string) {
		key = sourceQuestionKey(subject, key)
		value := subject.QuestionnaireAnswers[key]
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         key,
			Label:       label,
			Kind:        "select",
			Required:    true,
			Options:     append([]string{"unresolved"}, options...),
			Value:       value,
			Help:        help,
			NeedsReview: value == "" || value == "unresolved",
		})
	}
	for _, file := range subject.MediaFileFacts.Files {
		if file.ResourceID != "" && strings.EqualFold(file.Container, "ts") && file.Source == "" {
			add(
				"resource_source_"+file.ResourceID,
				"Broadcast source for resource "+file.FileName,
				"Verify the source records for this exact transport-stream resource; the container or release name does not establish a broadcast source.",
				"HDTV",
				"UHDTV",
				"SDTV",
				"other",
			)
		}
	}
	if normalizedLSTSource(subject.Source) == "WORKPRINT" ||
		slices.ContainsFunc(subject.MediaFileFacts.Files, func(file api.MediaFileFact) bool { return normalizedLSTSource(file.Source) == "WORKPRINT" }) {
		add(
			"workprint_retail",
			"Workprint retail release",
			"Verify this workprint was officially released at retail, rather than leaked or unofficial.",
			"official_retail",
			"unofficial",
		)
	}
	if needsModernEncodeReview(subject) {
		add(
			"encode_source",
			"Encode source provenance",
			"Check source records for an untouched retail source. A title or release-name label cannot establish this.",
			"retail_untouched",
			"not_retail_untouched",
		)
		add(
			"encode_rate_control",
			"Encode rate control",
			"Verify the encoding log/settings for every programme resource in this exact source and generation.",
			"crf",
			"two_pass_vbr",
			"other",
		)
		add(
			"encode_crop",
			"Encode black-bar cropping",
			"Inspect the output against the source. Imperfect cropping differs from leaving black bars entirely uncropped.",
			"no_black_bars",
			"cropped",
			"imperfect_crop",
			"entirely_uncropped",
		)
		if lstBelowHD(subject.Release.Resolution) || slices.ContainsFunc(subject.MediaFileFacts.Files, func(file api.MediaFileFact) bool {
			return lstBelowHD(file.Resolution) && !singleLegacyContainerCandidate(containerFileSubject(subject, file))
		}) {
			add(
				"encode_source_resolution",
				"Below-HD source resolution",
				"Verify that the original source itself is below HD; a lower-resolution intermediate is insufficient.",
				"source_below_hd",
				"source_supports_hd",
			)
		}
		if !singleLegacyContainerCandidate(subject) && lstUnknownEncoder(subject.VideoEncoder) {
			add(
				"encode_encoder",
				"Encode encoder identity",
				"Inspect the writing library or encoding log. H.264/H.265 identifies a codec, not its encoder.",
				"x264",
				"x265",
				"av1",
				"unsupported",
			)
		}
	}
	if needsModernEncodeReview(subject) {
		for i, file := range subject.MediaFileFacts.Files {
			if !singleLegacyContainerCandidate(containerFileSubject(subject, file)) && lstUnknownEncoder(file.VideoEncoder) {
				add(
					fmt.Sprintf("resource_encoder_%d", i),
					"Encoder identity for resource "+file.FileName,
					"Inspect this resource's writing library or encoding log; the codec does not establish its encoder.",
					"x264",
					"x265",
					"av1",
					"unsupported",
				)
			}
		}
	}
	if subject.Identity.Category == api.CanonicalCategoryTV && subject.EpisodeInt > 0 && !subject.TVPack && !endedLSTSeries(subject) {
		add(
			"episode_season",
			"Episode season availability",
			"Check the selected season's airing schedule. Individual episodes require a season that has not finished airing.",
			"season_still_airing",
			"season_finished",
		)
	}
	return fields
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
	add := func(rule, reason string, status api.MetadataEvidenceStatus, questionnaire bool) {
		failure := trackers.NewEvidenceRuleFailure("lst_"+rule, reason, api.RuleDispositionStrict, status)
		failure.DebugBypass = true
		failure.QuestionnaireInput = questionnaire
		failures = append(failures, failure)
	}
	if prohibitedLSTSource(subject.Source) {
		add("prohibited_source", "The established source is not permitted", api.MetadataEvidenceStatusComplete, false)
	}
	for _, file := range subject.MediaFileFacts.Files {
		if prohibitedLSTSource(file.Source) {
			add("resource_prohibited_source", "A programme resource has an established prohibited source", api.MetadataEvidenceStatusComplete, false)
		}
	}
	if subject.Identity.Category == api.CanonicalCategoryTV && subject.EpisodeInt > 0 && !subject.TVPack && endedLSTSeries(subject) {
		add("finished_season_episode", "The series has ended; upload a season pack instead of an individual episode", api.MetadataEvidenceStatusComplete, false)
	}
	if strings.EqualFold(subject.Type, "ENCODE") && !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) && !singleLegacyContainerCandidate(subject) {
		resolution := strings.ToLower(strings.TrimSpace(subject.Release.Resolution))
		encoder := lstEncoderIdentity(subject.VideoEncoder)
		if lstUnknownEncoder(encoder) {
			encoder = subject.QuestionnaireAnswers[sourceQuestionKey(subject, "encode_encoder")]
		}
		if encoder != "" && encoder != "unresolved" && !lstKnownEncoder(encoder) {
			add("encode_encoder", "The established encoder is not permitted", api.MetadataEvidenceStatusComplete, true)
		} else if (lstBelowHD(resolution) || resolution == "720p") && (encoder == "x265" || encoder == "av1" || slices.Contains([]string{"h.265", "hevc", "av1"}, strings.ToLower(subject.VideoCodec))) {
			add("encode_resolution_codec", "Encodes at 720p or below require x264", api.MetadataEvidenceStatusComplete, false)
		}
		if resolution == "" {
			add("encode_resolution_evidence", "Encode resolution is unresolved", api.MetadataEvidenceStatusPartial, false)
		}
		switch {
		case subject.HasEncodeSettings:
		case subject.Assessments.MediaInfoEncodeSettings == api.EncodeSettingsStatusMissing:
			add("encode_settings", "Encode settings are missing from MediaInfo", api.MetadataEvidenceStatusComplete, false)
		case subject.Assessments.MediaInfoEncodeSettings == api.EncodeSettingsStatusPresent:
		default:
			add("encode_settings_evidence", "Inspect MediaInfo to establish the required encode settings", api.MetadataEvidenceStatusPartial, false)
		}
	}
	if strings.EqualFold(subject.Type, "ENCODE") && !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		for i, file := range subject.MediaFileFacts.Files {
			current := containerFileSubject(subject, file)
			if singleLegacyContainerCandidate(current) {
				continue
			}
			if file.EncodeSettingsKnown && !file.HasEncodeSettings {
				add("resource_encode_settings", "A programme resource is missing encode settings", api.MetadataEvidenceStatusComplete, false)
			} else if !file.EncodeSettingsKnown {
				add(
					"resource_encode_settings_evidence",
					"Encode settings have not been inspected for a programme resource",
					api.MetadataEvidenceStatusPartial,
					false,
				)
			}

			resolution, encoder := strings.ToLower(strings.TrimSpace(file.Resolution)), lstEncoderIdentity(file.VideoEncoder)
			if lstUnknownEncoder(encoder) {
				encoder = subject.QuestionnaireAnswers[sourceQuestionKey(subject, fmt.Sprintf("resource_encoder_%d", i))]
			}
			if encoder != "unresolved" && !lstUnknownEncoder(encoder) && !lstKnownEncoder(encoder) {
				add("resource_encode_encoder", "A programme resource uses an unsupported encoder", api.MetadataEvidenceStatusComplete, false)
			}
			if (lstBelowHD(resolution) || resolution == "720p") &&
				(encoder == "x265" || encoder == "av1" || slices.Contains([]string{"h.265", "hevc", "av1"}, strings.ToLower(file.VideoCodec))) {
				add("resource_encode_resolution_codec", "A programme resource at 720p or below requires x264", api.MetadataEvidenceStatusComplete, false)
			}
		}
	}
	for _, field := range sourceReviewQuestions(subject) {
		value := subject.QuestionnaireAnswers[field.Key]
		status := api.MetadataEvidenceStatusPartial
		invalid := value == "" || value == "unresolved" || !slices.Contains(field.Options, value)
		if slices.Contains(
			[]string{
				"unofficial",
				"not_retail_untouched",
				"other",
				"entirely_uncropped",
				"source_supports_hd",
				"unsupported",
				"season_finished",
			},
			value,
		) {
			status = api.MetadataEvidenceStatusComplete
			invalid = true
		}
		if invalid {
			add("source_review", fmt.Sprintf("%s: %s", field.Label, field.Help), status, true)
		}
	}
	guidance := trackers.NewEvidenceRuleFailure(
		"lst_stream_processing",
		"Review source records for every prepared resource, including video, audio, subtitles and HDR layers. Distinguish studio/distributor processing already present at retail from user-generated/enhanced streams, and check for improper splicing.",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
	guidance.DebugBypass = true
	return append(failures, guidance)
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
