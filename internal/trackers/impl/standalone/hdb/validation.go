// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-hdb-constructibility-v3",
		Check: checkRequirements,
	}
}

// checkRequirements validates HDB upload constructibility from immutable
// evidence and reports incomplete prepared evidence as advisory failures.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	meta := standalone.UploadSubjectForValidation(subject)
	failures := make([]api.RuleFailure, 0, 12)
	if hdbCategoryID(meta) == 0 {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_category",
			"release does not map to an HDB category",
			api.RuleDispositionStrict,
		))
	}
	if hdbCodecID(meta) == 0 {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_codec",
			"video codec does not map to an HDB codec",
			api.RuleDispositionStrict,
		))
	}
	if hdbMediumID(meta) == 0 {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_medium",
			"release does not map to an HDB medium",
			api.RuleDispositionStrict,
		))
	}
	failures = append(failures, hdbMetadataBranchFailures(subject)...)
	failures = append(failures, trackers.ValidatePackageExtensions(subject.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:      hdbEvidencePolicy("hdb_package_safety"),
		BlockArchives: true,
		BlockedExtraKinds: []api.PackageFileKind{
			api.PackageFileKindSample,
			api.PackageFileKindNFO,
		},
	})...)
	if subject.Identity.Category == api.CanonicalCategoryTV && !subject.TVPack {
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			hdbEvidencePolicy("hdb_single_episode_layout"),
		)...)
	}
	failures = append(failures, trackers.ValidateMultiSeasonPackage(
		subject.PackageFacts,
		hdbEvidencePolicy("hdb_multi_season_pack"),
	)...)
	if !hdbDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateMediaConstraints(
			subject.MediaFileFacts,
			trackers.MediaConstraintPolicy{
				Evidence: hdbEvidencePolicy("hdb_supported_media"),
				AllowedVideoCodecs: []string{
					"AVC",
					"H.264",
					"MPEG-2",
					"VC-1",
					"XVID",
					"HEVC",
					"H.265",
					"VP9",
				},
			},
		)...)
	}
	failures = append(failures, hdbRequiredAssetFailures(subject)...)
	failures = append(failures, hdbTitleFailures(subject, meta)...)
	return failures, nil
}

var (
	hdbImageBBCodePattern = regexp.MustCompile(`(?is)\[img[^\]]*\].*?\[/img\]`)
	hdbBBCodeTagPattern   = regexp.MustCompile(`(?is)\[[^\]]+\]`)
)

// hdbMetadataBranchFailures permits a proved provider-unlisted release only
// with final description text and poster markup. Before descriptions are
// final, the missing manual assets remain advisory so the workflow can produce
// or collect them.
func hdbMetadataBranchFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	providerSubject := api.RuleSubject{
		SourcePath:       subject.SourcePath,
		Identity:         subject.Identity,
		ProviderMetadata: subject.ProviderMetadata,
	}
	var providerIDPresent bool
	var providerUnavailable bool
	switch subject.Identity.Category {
	case api.CanonicalCategoryMovie:
		providerIDPresent = subject.Identity.IMDBID > 0
		providerUnavailable = trackers.MetadataFieldPresent(trackers.MetadataFieldIMDBUnavailable, providerSubject)
	case api.CanonicalCategoryTV:
		providerIDPresent = subject.Identity.TVDBID > 0
		providerUnavailable = trackers.MetadataFieldPresent(trackers.MetadataFieldTVDBUnavailable, providerSubject)
	case api.CanonicalCategoryUnknown:
		return nil
	default:
		return nil
	}
	if providerIDPresent {
		return nil
	}

	manualAssetsReady := hdbManualMetadataAssetsReady(subject.DescriptionOverride)
	switch {
	case providerUnavailable && manualAssetsReady:
		return nil
	case providerUnavailable && !subject.DescriptionGroupsFinal:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_manual_metadata_assets_pending",
			"a provider-unlisted release requires a manual description with poster artwork before upload",
			api.RuleDispositionAdvisory,
			api.MetadataEvidenceStatusPartial,
		)}
	case providerUnavailable:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_manual_metadata_assets",
			"a provider-unlisted release requires a manual description with poster artwork",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusComplete,
		)}
	case manualAssetsReady:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_provider_unlisted_confirmation",
			"confirm the title has no provider entry before using manual description and poster artwork",
			api.RuleDispositionWaivable,
			api.MetadataEvidenceStatusUnavailable,
		)}
	default:
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_provider_or_manual_metadata",
			"a matching provider ID or manual description with poster artwork is required",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusUnavailable,
		)}
	}
}

func hdbManualMetadataAssetsReady(description string) bool {
	description = strings.TrimSpace(description)
	if description == "" || !hdbImageBBCodePattern.MatchString(description) {
		return false
	}
	withoutImages := hdbImageBBCodePattern.ReplaceAllString(description, " ")
	withoutTags := hdbBBCodeTagPattern.ReplaceAllString(withoutImages, " ")
	return strings.TrimSpace(withoutTags) != ""
}

func hdbEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func hdbRequiredAssetFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if subject.AssetFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_required_assets_evidence",
			"complete prepared-asset evidence is required",
			api.RuleDispositionAdvisory,
			subject.AssetFacts.Status,
		)}
	}
	kind := trackers.AssetKindMediaInfoText
	if hdbDiscType(subject.DiscType) {
		kind = trackers.AssetKindBDInfo
	}
	return trackers.ValidateRequiredAssets(subject.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence: hdbEvidencePolicy("hdb_required_assets"),
		Requirements: []trackers.AssetRequirement{{
			Kind:         kind,
			MinimumCount: 1,
		}},
	})
}

func hdbDiscType(value string) bool {
	switch strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(value)), " ", "") {
	case "BDMV", "HDDVD":
		return true
	default:
		return false
	}
}

func hdbTitleFailures(subject api.TrackerValidationSubject, meta api.UploadSubject) []api.RuleFailure {
	title, status := hdbTitleForValidation(subject, meta)
	if title == "" {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"hdb_title_prohibition",
			"resolved HDB title evidence is required",
			api.RuleDispositionAdvisory,
			status,
		)}
	}
	if !hdbTitleContainsProhibitedElement(title) {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"hdb_title_prohibition",
		"HDB title contains a prohibited naming element",
		api.RuleDispositionStrict,
		status,
	)}
}

func hdbTitleForValidation(subject api.TrackerValidationSubject, meta api.UploadSubject) (string, api.MetadataEvidenceStatus) {
	releaseName := strings.TrimSpace(subject.ReleaseName)
	sourceName := hdbSourceBaseName(subject.SourcePath)
	if releaseName != "" && sourceName != "" && strings.EqualFold(releaseName, sourceName) {
		return releaseName, api.MetadataEvidenceStatusComplete
	}
	if subject.Scene && !subject.SceneRenamed && releaseName != "" {
		return releaseName, api.MetadataEvidenceStatusComplete
	}
	if title := authoritativeHDBOriginalTitle(meta); title != "" {
		return buildGeneratedHDBName(meta, title), subject.ProvenanceFacts.Status
	}
	return "", subject.ProvenanceFacts.Status
}

func hdbSourceBaseName(sourcePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return ""
	}
	base := strings.TrimSpace(filepath.Base(sourcePath))
	switch strings.ToLower(filepath.Ext(base)) {
	case ".avi", ".m2ts", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".mts", ".ts", ".vob", ".webm", ".wmv":
		return strings.TrimSuffix(base, filepath.Ext(base))
	default:
		return base
	}
}
