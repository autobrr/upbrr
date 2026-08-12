// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns RF's source-backed package, media, and asset policy.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-rf-policy-v2", Check: checkEvidenceRules}
}

func checkEvidenceRules(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 8)
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:      rfEvidencePolicy("rf_archive"),
			BlockArchives: true,
		},
	)...)
	if !unit3d.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateMediaOnlyPackage(
			subject.PackageFacts,
			rfEvidencePolicy("rf_media_only"),
		)...)
		failures = append(failures, validateRFMovieFileCount(subject.PackageFacts)...)
		failures = append(failures, trackers.ValidateMediaConstraints(
			subject.MediaFileFacts,
			trackers.MediaConstraintPolicy{
				Evidence:           rfEvidencePolicy("rf_video_tracks"),
				MinVideoTrackCount: 1,
				MaxVideoTrackCount: 1,
			},
		)...)
	}
	failures = append(failures, trackers.ValidateRequiredAssets(
		subject.AssetFacts,
		trackers.RequiredAssetPolicy{
			Evidence:     rfEvidencePolicy("rf_asset"),
			Requirements: rfAssetRequirements(subject),
		},
	)...)
	if subject.SceneRenamed {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"rf_source_name",
			"source file or folder names must remain unchanged",
			api.RuleDispositionStrict,
			subject.ProvenanceFacts.Status,
		))
	}
	return failures, nil
}

func rfEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func validateRFMovieFileCount(facts api.PackageFacts) []api.RuleFailure {
	switch {
	case facts.Status != api.MetadataEvidenceStatusComplete:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"rf_movie_file_count",
			"complete package evidence is required to prove one movie per upload",
			api.RuleDispositionAdvisory,
			facts.Status,
		)}
	case facts.MediaFileCount != 1:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"rf_movie_file_count",
			"exactly one movie file is allowed",
			api.RuleDispositionStrict,
			facts.Status,
		)}
	default:
		return nil
	}
}

func rfAssetRequirements(subject api.TrackerValidationSubject) []trackers.AssetRequirement {
	infoKind := trackers.AssetKindMediaInfoText
	switch strings.ToUpper(strings.TrimSpace(subject.DiscType)) {
	case "BDMV":
		infoKind = trackers.AssetKindBDInfo
	case "DVD":
		infoKind = trackers.AssetKindDVDVOBMediaInfo
	}
	return []trackers.AssetRequirement{
		{
			Kind:         infoKind,
			MinimumCount: 1,
		},
		{
			Kind:         trackers.AssetKindScreenshot,
			MinimumCount: 3,
		},
	}
}
