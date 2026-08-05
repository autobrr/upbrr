// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns LUME's tracker-specific constructibility checks.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-lume-constructibility-v2",
		Check: checkRequirements,
	}
}

// checkRequirements enforces LUME's deterministic release requirements.
func checkRequirements(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 12)
	extraKinds := []api.PackageFileKind{
		api.PackageFileKindExternalSubtitle,
		api.PackageFileKindSample,
		api.PackageFileKindNFO,
		api.PackageFileKindChecksum,
		api.PackageFileKindImage,
		api.PackageFileKindText,
		api.PackageFileKindExecutable,
		api.PackageFileKindOther,
	}
	disc := unit3d.IsDiscType(meta.DiscType)
	if disc {
		extraKinds = nil
	} else if !strings.EqualFold(strings.TrimSpace(meta.Container), "mkv") {
		failures = append(failures, trackers.NewRuleFailure(
			"container",
			"LUME only allows MKV containers for non-disc uploads.",
			api.RuleDispositionStrict,
		))
	}
	failures = append(failures, trackers.ValidatePackageExtensions(meta.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:          lumeEvidencePolicy("lume_package_safety"),
		BlockArchives:     true,
		BlockedExtraKinds: extraKinds,
	})...)
	if !disc {
		failures = append(failures, trackers.ValidateSingleFileFolder(meta.PackageFacts, lumeEvidencePolicy("lume_single_file_layout"))...)
		failures = append(failures, trackers.ValidateMultiSeasonPackage(meta.PackageFacts, lumeEvidencePolicy("lume_multi_season_non_disc"))...)
		failures = append(failures, trackers.ValidatePerFileUniformity(meta.MediaFileFacts, trackers.PerFileUniformityPolicy{
			Evidence: lumeEvidencePolicy("lume_media_uniformity"),
			Fields: []trackers.MediaUniformityField{
				trackers.MediaUniformityFieldContainer,
				trackers.MediaUniformityFieldSource,
				trackers.MediaUniformityFieldResolution,
				trackers.MediaUniformityFieldVideoCodec,
				trackers.MediaUniformityFieldBitDepth,
				trackers.MediaUniformityFieldVideoTrackCount,
			},
		})...)
		failures = append(failures, trackers.ValidateMediaConstraints(meta.MediaFileFacts, trackers.MediaConstraintPolicy{
			Evidence:           lumeEvidencePolicy("lume_media_constraints"),
			MinVideoTrackCount: 1,
			MaxVideoTrackCount: 1,
		})...)
		failures = append(failures, trackers.ValidateLanguageCombination(meta.MediaFileFacts, trackers.LanguageCombinationPolicy{
			Evidence:               lumeEvidencePolicy("lume_language"),
			RequireOriginalAudio:   true,
			RequireEnglishSubtitle: true,
		})...)
		failures = append(failures, lumeResolutionFailures(meta)...)
	}
	failures = append(failures, lumeRequiredAssetFailures(meta)...)
	return failures, nil
}

func lumeRequiredAssetFailures(meta api.TrackerValidationSubject) []api.RuleFailure {
	if meta.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"lume_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			meta.AssetFacts.Status,
		)}
	}
	return trackers.ValidateRequiredAssets(meta.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence:     lumeEvidencePolicy("lume_required_assets"),
		Requirements: lumeAssetRequirements(meta.DiscType),
	})
}

func lumeEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func lumeResolutionFailures(meta api.TrackerValidationSubject) []api.RuleFailure {
	resolution := unit3d.RuleResolution(unit3d.ValidationRuleSubject(meta))
	if resolution == "" {
		return []api.RuleFailure{
			trackers.NewRuleFailure(
				"resolution_required",
				"LUME requires a known resolution",
				api.RuleDispositionStrict,
			),
			trackers.NewEvidenceRuleFailure(
				"lume_resolution_evidence",
				"known resolution evidence is required",
				api.RuleDispositionAdvisory,
				meta.MediaFileFacts.TechnicalStatus,
			),
		}
	}
	if !unit3d.ResolutionBelow(resolution, "720p") {
		return nil
	}
	return []api.RuleFailure{
		trackers.NewRuleFailure(
			"min_resolution",
			"LUME only allows SD releases when the content does not have a higher resolution release.",
			api.RuleDispositionStrict,
		),
		trackers.NewEvidenceRuleFailure(
			"lume_lower_resolution_availability",
			"sub-720p releases require manual proof that no higher-resolution release exists",
			api.RuleDispositionAdvisory,
			meta.AvailabilityFacts.Status,
		),
	}
}

func lumeAssetRequirements(discType string) []trackers.AssetRequirement {
	requirements := []trackers.AssetRequirement{{
		Kind:         trackers.AssetKindHostedScreenshot,
		MinimumCount: 3,
	}}
	if strings.EqualFold(strings.TrimSpace(discType), "BDMV") {
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	}
	return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
}
