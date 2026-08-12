// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-btn-constructibility-v2",
		Check: checkRequirements,
	}
}

func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	meta := standalone.UploadSubjectForValidation(subject)
	failures := make([]api.RuleFailure, 0, 12)
	if reason := btnTVPayloadMetadataMessage(meta); reason != "" {
		failures = append(failures, trackers.NewRuleFailure("canonical_tv_metadata", reason, api.RuleDispositionStrict))
	}
	failures = append(failures, trackers.ValidatePackageExtensions(subject.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:      btnEvidencePolicy("btn_package_safety", api.RuleDispositionStrict),
		BlockArchives: true,
		BlockedExtraKinds: []api.PackageFileKind{
			api.PackageFileKindExternalSubtitle,
			api.PackageFileKindSample,
			api.PackageFileKindProof,
			api.PackageFileKindNFO,
			api.PackageFileKindChecksum,
			api.PackageFileKindImage,
			api.PackageFileKindText,
			api.PackageFileKindExecutable,
			api.PackageFileKindOther,
		},
	})...)
	if !trackers.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			btnEvidencePolicy("btn_single_file_layout", api.RuleDispositionStrict),
		)...)
	}
	failures = append(failures, trackers.ValidateMultiSeasonPackage(
		subject.PackageFacts,
		btnEvidencePolicy("btn_multi_season_package", api.RuleDispositionStrict),
	)...)
	if subject.TVPack {
		failures = append(failures, trackers.ValidateCompleteEpisodeRange(subject.PackageFacts, trackers.EpisodeRangePolicy{
			Evidence: btnEvidencePolicy("btn_episode_range", api.RuleDispositionStrict),
			Season:   subject.SeasonInt,
		})...)
		failures = append(failures, trackers.ValidatePerFileUniformity(subject.MediaFileFacts, trackers.PerFileUniformityPolicy{
			Evidence: btnEvidencePolicy("btn_pack_uniformity", api.RuleDispositionWaivable),
			Fields: []trackers.MediaUniformityField{
				trackers.MediaUniformityFieldContainer,
				trackers.MediaUniformityFieldSource,
				trackers.MediaUniformityFieldResolution,
				trackers.MediaUniformityFieldVideoCodec,
				trackers.MediaUniformityFieldVideoEncode,
			},
		})...)
		if seasonPackHasMixedGroups(meta) {
			failures = append(failures, trackers.NewEvidenceRuleFailure(
				"btn_mixed_pack",
				"mixed-group season packs require staff approval",
				api.RuleDispositionWaivable,
				subject.PackageFacts.Status,
			))
		}
	}
	failures = append(failures, trackers.ValidateMediaConstraints(subject.MediaFileFacts, trackers.MediaConstraintPolicy{
		Evidence: btnEvidencePolicy("btn_media_constraints", api.RuleDispositionStrict),
		AllowedContainers: []string{
			"avi", "matroska", "mkv", "vob", "mpeg", "mpg", "mp4", "iso", "wmv", "ts", "m4v", "m2ts", "mixed",
		},
		AllowedVideoCodecs: []string{
			"xvid", "divx", "mpeg-2", "mpeg2", "vc-1", "wmv", "vp9", "avc", "h.264", "h264", "x264", "hevc", "h.265", "h265", "x265",
			"dvdr", "bd", "x264-hi10p", "mixed",
		},
		AllowedSources: []string{
			"hdtv", "pdtv", "dsr", "dvdrip", "tvrip", "vhsrip", "bluray", "blu-ray", "bdrip", "brrip", "dvd5", "dvd9", "hddvd",
			"web", "web-dl", "webdl", "webrip", "bd5", "bd9", "bd25", "bd50", "mixed", "unknown",
		},
		AllowedResolutions: []string{
			"sd", "480i", "480p", "576i", "576p", "720p", "1080i", "1080p", "1440p", "2160p", "4320p", "8640p", "portable device", "mixed",
		},
	})...)
	failures = append(failures, btnForbiddenMediaFailures(subject)...)
	return failures, nil
}

func btnEvidencePolicy(rule string, violation api.RuleDisposition) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       violation,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func btnForbiddenMediaFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	typeValue := strings.ToLower(strings.TrimSpace(subject.Type))
	source := strings.ToLower(strings.TrimSpace(subject.Source))
	container := strings.ToLower(strings.TrimSpace(subject.Container))
	discType := strings.ToLower(strings.TrimSpace(subject.DiscType))
	switch {
	case strings.Contains(typeValue, "remux") && (strings.Contains(source, "dvd") || discType == "dvd"):
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"btn_dvd_remux",
			"DVD remuxes are not allowed",
			api.RuleDispositionStrict,
			subject.MediaFileFacts.TechnicalStatus,
		)}
	case subject.Scene && discType == "dvd" && container == "iso":
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"btn_scene_dvd_image",
			"Scene DVD image uploads are not allowed",
			api.RuleDispositionStrict,
			subject.MediaFileFacts.TechnicalStatus,
		)}
	default:
		return nil
	}
}
