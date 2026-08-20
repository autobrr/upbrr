// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validateAZConstructibility(site siteDefinition, subject api.TrackerValidationSubject) []api.RuleFailure {
	meta := azUploadSubject(subject)
	failures := make([]api.RuleFailure, 0, 3)
	if strings.TrimSpace(categoryID(meta)) == "" || strings.TrimSpace(categorySlug(meta)) == "" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_category",
			"Tracker does not support the canonical release category.",
			api.RuleDispositionStrict,
		))
	}
	if ripTypeID(meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_type",
			"Tracker does not support the release type.",
			api.RuleDispositionStrict,
		))
	}
	if videoQualityID(site, meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_resolution",
			"Tracker does not support the release resolution.",
			api.RuleDispositionStrict,
		))
	}
	return failures
}

func azUploadSubject(subject api.TrackerValidationSubject) api.UploadSubject {
	return api.UploadSubject{
		SourcePath:           subject.SourcePath,
		DiscType:             subject.DiscType,
		Tag:                  subject.Tag,
		Release:              subject.Release,
		Identity:             subject.Identity,
		ProviderMetadata:     subject.ProviderMetadata,
		SeasonInt:            subject.SeasonInt,
		EpisodeInt:           subject.EpisodeInt,
		TVPack:               subject.TVPack,
		Anime:                subject.Anime,
		AudioLanguages:       append([]string(nil), subject.AudioLanguages...),
		SubtitleLanguages:    append([]string(nil), subject.SubtitleLanguages...),
		Container:            subject.Container,
		Source:               subject.Source,
		Type:                 subject.Type,
		HDR:                  subject.HDR,
		Region:               subject.Region,
		VideoCodec:           subject.VideoCodec,
		VideoEncode:          subject.VideoEncode,
		BitDepth:             subject.BitDepth,
		Edition:              subject.Edition,
		WebDV:                subject.WebDV,
		Service:              subject.Service,
		ServiceLongName:      subject.ServiceLongName,
		ReleaseName:          subject.ReleaseName,
		ReleaseNameNoTag:     subject.ReleaseNameNoTag,
		ReleaseNameOverrides: subject.ReleaseNameOverrides,
	}
}

// validateAZEvidence enforces site-specific package, media, language, and
// prepared-asset predicates from the validation evidence snapshot.
func validateAZEvidence(site siteDefinition, subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := make([]api.RuleFailure, 0, 8)
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:      azEvidencePolicy("azfamily_archive", api.RuleDispositionStrict),
			BlockArchives: true,
		},
	)...)
	if !trackers.IsDiscType(subject.DiscType) {
		extraDisposition := api.RuleDispositionStrict
		if site.Name == "CZ" {
			extraDisposition = api.RuleDispositionWaivable
		}
		failures = append(failures, trackers.ValidateMediaOnlyPackage(
			subject.PackageFacts,
			azEvidencePolicy("azfamily_media_only", extraDisposition),
		)...)
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			azEvidencePolicy("azfamily_single_file_folder", api.RuleDispositionWaivable),
		)...)
		failures = append(failures, trackers.ValidateMediaConstraints(
			subject.MediaFileFacts,
			azMediaConstraintPolicy(site, subject),
		)...)
		failures = append(failures, trackers.ValidateLanguageCombination(
			subject.MediaFileFacts,
			azLanguagePolicy(site),
		)...)
	}
	failures = append(failures, trackers.ValidateRequiredAssets(
		subject.AssetFacts,
		trackers.RequiredAssetPolicy{
			Evidence:     azEvidencePolicy("azfamily_asset", api.RuleDispositionStrict),
			Requirements: azAssetRequirements(site, subject),
		},
	)...)
	return failures
}

func azEvidencePolicy(rule string, violationDisposition api.RuleDisposition) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       violationDisposition,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func azMediaConstraintPolicy(site siteDefinition, subject api.TrackerValidationSubject) trackers.MediaConstraintPolicy {
	containers := []string{"MKV", "MP4", "AVI"}
	codecs := []string{"H.264", "H264", "x264", "AVC", "H.265", "H265", "x265", "HEVC", "DivX", "Xvid"}
	if site.Name == "CZ" {
		codecs = append(codecs, "VP9")
	}
	if strings.EqualFold(strings.TrimSpace(subject.Source), "HDTV") {
		containers = append(containers, "TS", "TP")
		codecs = append(codecs, "MPEG-2", "MPEG2")
	}
	return trackers.MediaConstraintPolicy{
		Evidence:           azEvidencePolicy("azfamily_media_constraints", api.RuleDispositionStrict),
		AllowedContainers:  containers,
		AllowedVideoCodecs: codecs,
	}
}

func azLanguagePolicy(site siteDefinition) trackers.LanguageCombinationPolicy {
	policy := trackers.LanguageCombinationPolicy{
		Evidence: azEvidencePolicy("azfamily_language", api.RuleDispositionStrict),
	}
	if site.Name == "AZ" {
		policy.RequireOriginalOrEnglishAudio = true
	} else {
		policy.RequireOriginalAudio = true
	}
	policy.RequireEnglishSubtitleWithoutAudio = true
	return policy
}

func azAssetRequirements(site siteDefinition, subject api.TrackerValidationSubject) []trackers.AssetRequirement {
	infoKind := trackers.AssetKindMediaInfoText
	if trackers.IsDiscType(subject.DiscType) {
		infoKind = trackers.AssetKindBDInfo
	}
	screenshotCount := azScreenshotMinimum(site, subject)
	return []trackers.AssetRequirement{
		{
			Kind:         infoKind,
			MinimumCount: 1,
		},
		{
			Kind:         trackers.AssetKindScreenshot,
			MinimumCount: screenshotCount,
		},
	}
}

func azScreenshotMinimum(site siteDefinition, subject api.TrackerValidationSubject) int {
	if site.Name == "CZ" &&
		(trackers.IsDiscType(subject.DiscType) ||
			strings.EqualFold(strings.TrimSpace(subject.Type), "REMUX") ||
			strings.EqualFold(strings.TrimSpace(subject.Release.Type), "REMUX") ||
			strings.Contains(strings.ToLower(subject.Release.Resolution), "2160")) {
		return 6
	}
	return 3
}
