// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

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
		ID: "unit3d-lst-payload-v17",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			failures := lstDeterministicFailures(subject)
			failures = append(failures, trackers.NewEvidenceRuleFailure(
				"guidance_lst_duplicate_evidence",
				"Review source and comparison evidence against the tracker's current rules. Unknown quality, source history, internal status or replacement eligibility is not established by an acknowledgement; independent duplicate safeguards still apply.",
				api.RuleDispositionAdvisory,
				api.MetadataEvidenceStatusPartial,
			))
			failures = append(failures, presentationReviewFailures(subject)...)
			failures = append(failures, audioTierFailures(subject)...)
			failures = append(failures, audioProcessingGuidance(subject)...)
			failures = append(failures, exceptionValidationFailures(subject)...)
			failures = append(failures, assetReviewFailures(subject)...)
			failures = append(failures, sourceValidationFailures(subject)...)
			failures = append(failures, discReviewFailures(subject)...)
			return failures, nil
		},
	}
}

// lstDeterministicFailures enforces prohibited-content and parser requirements.
// Screenshot deficiencies are reviewed through evidence-bound warnings instead.
func lstDeterministicFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
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
	if unit3d.IsDiscType(subject.DiscType) {
		extraKinds = nil
	}
	failures := trackers.ValidatePackageExtensions(subject.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "lst_package_safety",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		BlockArchives:     true,
		BlockedExtraKinds: extraKinds,
	})
	if subject.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return append(failures, trackers.NewEvidenceRuleFailure(
			"lst_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			subject.AssetFacts.Status,
		))
	}
	return append(failures, trackers.ValidateRequiredAssets(subject.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence: trackers.EvidencePredicatePolicy{
			Rule:                       "lst_required_assets",
			ViolationDisposition:       api.RuleDispositionStrict,
			MissingEvidenceDisposition: api.RuleDispositionAdvisory,
		},
		Requirements: lstAssetRequirements(subject.DiscType),
	})...)
}

func lstAssetRequirements(discType string) []trackers.AssetRequirement {
	switch strings.ToUpper(strings.TrimSpace(discType)) {
	case "BDMV", "HDDVD":
		return []trackers.AssetRequirement{{Kind: trackers.AssetKindBDInfo}}
	case "DVD":
		return []trackers.AssetRequirement{{Kind: trackers.AssetKindMediaInfoText}, {Kind: trackers.AssetKindDVDVOBMediaInfo}}
	default:
		return []trackers.AssetRequirement{{Kind: trackers.AssetKindMediaInfoText}}
	}
}
