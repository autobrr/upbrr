// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns AITHER's source-backed package, language, and
// prepared-asset policy.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-aither-policy-v2", Check: checkEvidenceRules}
}

func checkEvidenceRules(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}

	failures := make([]api.RuleFailure, 0, 8)
	failures = append(failures, trackers.ValidatePackageExtensions(subject.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:      aitherEvidencePolicy("aither_archive"),
		BlockArchives: true,
	})...)
	if !unit3d.IsDiscType(subject.DiscType) && !subject.Anime {
		failures = append(failures, trackers.ValidateMediaOnlyPackage(
			subject.PackageFacts,
			aitherEvidencePolicy("aither_media_only"),
		)...)
	}
	if subject.TVPack || subject.Identity.Category == api.CanonicalCategoryTV {
		failures = append(failures, trackers.ValidateMultiSeasonPackage(
			subject.PackageFacts,
			aitherEvidencePolicy("aither_multi_season"),
		)...)
	}
	if !unit3d.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateLanguageCombination(
			subject.MediaFileFacts,
			trackers.LanguageCombinationPolicy{
				Evidence:                           aitherEvidencePolicy("aither_language"),
				RequireOriginalAudio:               true,
				RequireEnglishSubtitleWithoutAudio: true,
			},
		)...)
	}
	failures = append(failures, trackers.ValidateRequiredAssets(
		subject.AssetFacts,
		trackers.RequiredAssetPolicy{
			Evidence:     aitherEvidencePolicy("aither_asset"),
			Requirements: aitherAssetRequirements(subject),
		},
	)...)
	if subject.SceneRenamed {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"aither_source_name",
			"source file or folder names must remain unchanged",
			api.RuleDispositionStrict,
			subject.ProvenanceFacts.Status,
		))
	}
	return failures, nil
}

func aitherEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func aitherAssetRequirements(subject api.TrackerValidationSubject) []trackers.AssetRequirement {
	requirements := []trackers.AssetRequirement{{
		Kind:         trackers.AssetKindScreenshot,
		MinimumCount: 3,
	}}
	switch strings.ToUpper(strings.TrimSpace(subject.DiscType)) {
	case "BDMV", "HDDVD":
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	case "DVD":
		return append(requirements,
			trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText},
			trackers.AssetRequirement{Kind: trackers.AssetKindDVDVOBMediaInfo},
		)
	default:
		return append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
	}
}
