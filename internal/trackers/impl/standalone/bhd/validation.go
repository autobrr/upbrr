// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-bhd-constructibility-v2",
		Check: checkRequirements,
	}
}

// checkRequirements enforces rules 1.2.3-1.2.4, 1.2.9, 4.1.1-4.1.3, and 4.2.1.
func checkRequirements(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 8)
	if _, ok := Source(meta.Source); !ok {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_source",
			"BHD does not support the release source.",
			api.RuleDispositionStrict,
		))
	}
	if meta.Identity.TMDBID <= 0 {
		failures = append(failures, trackers.NewRuleFailure(
			"required_provider_id",
			"BHD requires a canonical TMDB ID.",
			api.RuleDispositionStrict,
		))
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX", "ENCODE", "WEBDL", "WEBRIP":
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			failures = append(failures, trackers.NewRuleFailure(
				"container",
				fmt.Sprintf(
					"Container %q is not allowed for %s. Only MKV and MP4 are permitted.",
					meta.Container,
					strings.ToUpper(strings.TrimSpace(meta.Type)),
				),
				api.RuleDispositionStrict,
			))
		}
	}

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
	if bhdDiscType(meta.DiscType) {
		extraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(meta.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "bhd_package_safety",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		BlockArchives:     true,
		BlockedExtraKinds: extraKinds,
	})...)
	failures = append(failures, trackers.ValidateMultiSeasonPackage(meta.PackageFacts, trackers.EvidencePredicatePolicy{
		Rule:                       "bhd_multi_season_pack",
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	})...)
	failures = append(failures, bhdRequiredAssetFailures(meta)...)
	return failures, nil
}

// bhdRequiredAssetFailures reports advisory evidence gaps separately from
// strict asset requirement violations.
func bhdRequiredAssetFailures(meta api.TrackerValidationSubject) []api.RuleFailure {
	if meta.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"bhd_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			meta.AssetFacts.Status,
		)}
	}
	return trackers.ValidateRequiredAssets(meta.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "bhd_required_assets",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		Requirements: bhdAssetRequirements(meta.DiscType),
	})
}

func bhdDiscType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BDMV", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

func bhdAssetRequirements(discType string) []trackers.AssetRequirement {
	requirements := []trackers.AssetRequirement{{
		Kind:         trackers.AssetKindHostedScreenshot,
		MinimumCount: 3,
	}}
	switch strings.ToUpper(strings.TrimSpace(discType)) {
	case "BDMV", "HDDVD":
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	case "DVD":
		return append(
			requirements,
			trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText},
			trackers.AssetRequirement{Kind: trackers.AssetKindDVDVOBMediaInfo},
		)
	default:
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
	}
}
