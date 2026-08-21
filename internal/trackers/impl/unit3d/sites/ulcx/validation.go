// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns ULCX's tracker-specific semantic checks.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-ulcx-policy-v3",
		Check: checkRules,
	}
}

// checkRules enforces general, video, subtitles/audio, screenshots, and
// description requirements.
func checkRules(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 10)
	ruleSubject := unit3d.ValidationRuleSubject(meta)
	if unit3d.ContainsRuleValue(unit3d.RuleKeywords(ruleSubject), []string{"concert"}) {
		failures = append(failures, trackers.NewRuleFailure("block_concert", "Concerts not allowed at ULCX.", api.RuleDispositionWaivable))
	}
	disc := unit3d.IsDiscType(meta.DiscType)
	extraKinds := []api.PackageFileKind{
		api.PackageFileKindExternalSubtitle,
		api.PackageFileKindSample,
		api.PackageFileKindProof,
		api.PackageFileKindNFO,
		api.PackageFileKindChecksum,
		api.PackageFileKindImage,
		api.PackageFileKindText,
		api.PackageFileKindExecutable,
		api.PackageFileKindOther,
	}
	if disc {
		extraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(meta.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:          ulcxEvidencePolicy("ulcx_package_safety"),
		BlockArchives:     true,
		BlockedExtraKinds: extraKinds,
	})...)
	if !disc {
		failures = append(failures, ulcxContainerFailures(meta, ruleSubject)...)
		failures = append(failures, trackers.ValidateSingleFileFolder(meta.PackageFacts, ulcxEvidencePolicy("ulcx_single_file_layout"))...)
		failures = append(failures, trackers.ValidateMultiSeasonPackage(meta.PackageFacts, ulcxEvidencePolicy("ulcx_multi_season_non_disc"))...)
		failures = append(failures, trackers.ValidateLanguageCombination(meta.MediaFileFacts, trackers.LanguageCombinationPolicy{
			Evidence:                           ulcxEvidencePolicy("ulcx_language"),
			RequireOriginalOrEnglishAudio:      true,
			RequireEnglishSubtitleWithoutAudio: true,
		})...)
	}
	failures = append(failures, ulcxRequiredAssetFailures(meta)...)
	failures = append(failures, ulcxEncodeFailures(meta, ruleSubject)...)
	failures = append(failures, ulcxAudioFailures(meta, ruleSubject)...)
	return failures, nil
}

func ulcxContainerFailures(meta api.TrackerValidationSubject, ruleSubject api.RuleSubject) []api.RuleFailure {
	for _, extension := range meta.PackageFacts.Extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != ".mkv" && (extension != ".ts" || unit3d.RuleType(ruleSubject) != "HDTV") {
			return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
				"ulcx_media_container",
				"non-disc releases must contain only MKV files, except TS files for HDTV",
				api.RuleDispositionStrict,
				meta.PackageFacts.Status,
			)}
		}
	}
	return nil
}

func ulcxRequiredAssetFailures(meta api.TrackerValidationSubject) []api.RuleFailure {
	if meta.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			meta.AssetFacts.Status,
		)}
	}
	return trackers.ValidateRequiredAssets(meta.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence:     ulcxEvidencePolicy("ulcx_required_assets"),
		Requirements: ulcxAssetRequirements(meta.DiscType),
	})
}

func ulcxEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func ulcxEncodeFailures(meta api.TrackerValidationSubject, ruleSubject api.RuleSubject) []api.RuleFailure {
	typeValue := unit3d.RuleType(ruleSubject)
	resolution := unit3d.RuleResolution(ruleSubject)
	if resolution == "" && typeValue == "ENCODE" {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_encode_resolution_evidence",
			"encode resolution evidence is required",
			api.RuleDispositionAdvisory,
			meta.MediaFileFacts.TechnicalStatus,
		)}
	}
	failures := make([]api.RuleFailure, 0, 2)
	if (typeValue == "ENCODE" || typeValue == "HDTV") && unit3d.ResolutionBelow(resolution, "720p") {
		failures = append(failures, trackers.NewRuleFailure(
			"encode_min_resolution",
			"Encodes must be at least 720p resolution for ULCX.",
			api.RuleDispositionStrict,
		))
	}
	if typeValue == "ENCODE" && ulcxX265Encode(meta) {
		failures = append(failures, ulcxX265SourceFailures(meta, unit3d.Animation(ruleSubject) || unit3d.Anime(ruleSubject))...)
	}
	if typeValue == "ENCODE" && ulcxAV1Encode(meta) && !unit3d.Animation(ruleSubject) && !unit3d.Anime(ruleSubject) {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"ulcx_av1_animation_only",
			"AV1 encodes are allowed only for animated content",
			api.RuleDispositionStrict,
			meta.MediaFileFacts.TechnicalStatus,
		))
	}
	return failures
}

func ulcxAV1Encode(meta api.TrackerValidationSubject) bool {
	return strings.EqualFold(strings.TrimSpace(meta.VideoEncode), "AV1") ||
		strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "AV1")
}

func ulcxAudioFailures(meta api.TrackerValidationSubject, ruleSubject api.RuleSubject) []api.RuleFailure {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	channels, channelsKnown := ulcxChannelCount(meta.Channels)
	status := meta.MediaFileFacts.TechnicalStatus
	switch {
	case strings.Contains(audio, "LPCM"):
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_lpcm_audio",
			"LPCM audio is not allowed",
			api.RuleDispositionStrict,
			status,
		)}
	case strings.Contains(audio, "FLAC") && channelsKnown && channels > 2:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_flac_channels",
			"FLAC audio is allowed only for mono or stereo tracks",
			api.RuleDispositionStrict,
			status,
		)}
	case unit3d.RuleType(ruleSubject) == "ENCODE" && channelsKnown && channels > 2 &&
		ulcxLosslessAudio(audio) && unit3d.ResolutionBelow(unit3d.RuleResolution(ruleSubject), "2160p"):
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_encode_lossless_multichannel",
			"lossless multichannel audio is not allowed on encodes at 1080p or below",
			api.RuleDispositionStrict,
			status,
		)}
	default:
		return nil
	}
}

func ulcxChannelCount(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	end := 0
	for end < len(value) && ((value[end] >= '0' && value[end] <= '9') || value[end] == '.') {
		end++
	}
	if end == 0 {
		return 0, false
	}
	channels, err := strconv.ParseFloat(value[:end], 64)
	return channels, err == nil
}

func ulcxLosslessAudio(audio string) bool {
	pcm := slices.Contains(strings.FieldsFunc(audio, func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	}), "PCM")
	return strings.Contains(audio, "FLAC") || strings.Contains(audio, "TRUEHD") ||
		strings.Contains(audio, "DTS-HD MA") || strings.Contains(audio, "DTS-HD MASTER") ||
		strings.Contains(audio, "LPCM") || pcm || strings.Contains(audio, "ALAC")
}

func ulcxX265Encode(meta api.TrackerValidationSubject) bool {
	if strings.EqualFold(strings.TrimSpace(meta.VideoEncode), "x265") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") &&
		strings.EqualFold(strings.TrimSpace(meta.Type), "ENCODE")
}

func ulcxX265SourceFailures(meta api.TrackerValidationSubject, animation bool) []api.RuleFailure {
	if len(meta.MediaFileFacts.Files) == 0 {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_x265_source",
			"x265 encodes require source-resolution evidence",
			api.RuleDispositionAdvisory,
			meta.MediaFileFacts.TechnicalStatus,
		)}
	}
	missing := false
	for _, file := range meta.MediaFileFacts.Files {
		source := strings.ToLower(strings.TrimSpace(file.Source))
		switch {
		case strings.Contains(source, "uhd"), strings.Contains(source, "2160"):
			continue
		case animation && ulcxExplicitHDSource(source):
			continue
		case ulcxExplicitIneligibleX265Source(source, animation):
			return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
				"hevc_resolution_2160p",
				"x265 source resolution is not allowed",
				api.RuleDispositionStrict,
				meta.MediaFileFacts.TechnicalStatus,
			)}
		default:
			missing = true
		}
	}
	if missing || meta.MediaFileFacts.ExpectedFileCount <= 0 ||
		len(meta.MediaFileFacts.Files) != meta.MediaFileFacts.ExpectedFileCount ||
		meta.MediaFileFacts.TechnicalStatus != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"ulcx_x265_source",
			"x265 encodes require complete source-resolution evidence",
			api.RuleDispositionAdvisory,
			meta.MediaFileFacts.TechnicalStatus,
		)}
	}
	return nil
}

func ulcxExplicitHDSource(source string) bool {
	return strings.Contains(source, "1080") || strings.Contains(source, "720") ||
		strings.Contains(source, "hd source")
}

func ulcxExplicitIneligibleX265Source(source string, animation bool) bool {
	if strings.Contains(source, "dvd") || strings.Contains(source, "576") ||
		strings.Contains(source, "480") || strings.Contains(source, "sd source") {
		return true
	}
	return !animation && ulcxExplicitHDSource(source)
}

func ulcxAssetRequirements(discType string) []trackers.AssetRequirement {
	requirements := []trackers.AssetRequirement{{
		Kind:         trackers.AssetKindHostedScreenshot,
		MinimumCount: 3,
	}}
	if strings.EqualFold(strings.TrimSpace(discType), "BDMV") {
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	}
	return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
}
