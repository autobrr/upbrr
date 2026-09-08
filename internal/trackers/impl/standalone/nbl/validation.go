// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-nbl-policy-v3",
		Check: checkEvidenceRules,
	}
}

func checkEvidenceRules(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 8)
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:      nblEvidencePolicy("nbl_archive"),
			BlockArchives: true,
		},
	)...)
	if !trackers.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateMediaOnlyPackage(
			subject.PackageFacts,
			nblEvidencePolicy("nbl_media_only"),
		)...)
	}
	if !subject.TVPack {
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			nblEvidencePolicy("nbl_episode_layout"),
		)...)
	}
	failures = append(failures, trackers.ValidateMultiSeasonPackage(
		subject.PackageFacts,
		nblEvidencePolicy("nbl_multi_season"),
	)...)
	failures = append(failures, trackers.ValidateRequiredAssets(
		subject.AssetFacts,
		trackers.RequiredAssetPolicy{
			Evidence: nblEvidencePolicy("nbl_asset"),
			Requirements: []trackers.AssetRequirement{{
				Kind:         trackers.AssetKindMediaInfoText,
				MinimumCount: 1,
			}},
		},
	)...)
	if !trackers.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateLanguageCombination(
			subject.MediaFileFacts,
			trackers.LanguageCombinationPolicy{
				Evidence:                           nblEvidencePolicy("nbl_language"),
				RequireEnglishSubtitleWithoutAudio: true,
			},
		)...)
	}
	return failures, nil
}

func nblEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}
