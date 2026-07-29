// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-hhd-deterministic-v1",
		Check: checkRequirements,
	}
}

// checkRequirements enforces single-file layout, prohibited extras,
// MediaInfo/BDInfo, and screenshot requirements.
func checkRequirements(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 8)
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
	if unit3d.IsDiscType(meta.DiscType) {
		extraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(meta.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "hhd_package_safety",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		BlockArchives:     true,
		BlockedExtraKinds: extraKinds,
	})...)
	failures = append(failures, trackers.ValidateSingleFileFolder(meta.PackageFacts, trackers.EvidencePredicatePolicy{
		Rule:                       "hhd_single_file_layout",
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	})...)
	if !unit3d.IsDiscType(meta.DiscType) {
		failures = append(failures, trackers.ValidateMultiSeasonPackage(meta.PackageFacts, trackers.EvidencePredicatePolicy{
			Rule:                       "hhd_multi_season_non_disc",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		})...)
	}
	failures = append(failures, hhdRequiredAssetFailures(meta)...)
	return failures, nil
}

func hhdRequiredAssetFailures(meta api.TrackerValidationSubject) []api.RuleFailure {
	if meta.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hhd_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			meta.AssetFacts.Status,
		)}
	}
	return trackers.ValidateRequiredAssets(meta.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "hhd_required_assets",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		Requirements: hhdAssetRequirements(meta),
	})
}

func hhdAssetRequirements(meta api.TrackerValidationSubject) []trackers.AssetRequirement {
	requirements := make([]trackers.AssetRequirement, 0, 3)
	if !strings.EqualFold(strings.TrimSpace(meta.Container), "iso") {
		requirements = append(requirements, trackers.AssetRequirement{
			Kind:         trackers.AssetKindHostedScreenshot,
			MinimumCount: 3,
		})
	}
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	case "DVD":
		return append(
			requirements,
			trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText},
			trackers.AssetRequirement{Kind: trackers.AssetKindDVDVOBMediaInfo},
		)
	case "HDDVD":
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
	default:
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
	}
}
