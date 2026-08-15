// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationPolicy returns SP's source-backed package, content, pack, disc,
// and resolution checks.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-sp-policy-v4",
		Check: checkRequirements,
	}
}

// checkRequirements enforces upload and torrent-organization requirements.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 14)
	disc := unit3d.IsDiscType(subject.DiscType)
	extraKinds := []api.PackageFileKind{
		api.PackageFileKindExternalSubtitle,
		api.PackageFileKindSample,
		api.PackageFileKindProof,
		api.PackageFileKindNFO,
		api.PackageFileKindChecksum,
		api.PackageFileKindImage,
		api.PackageFileKindText,
		api.PackageFileKindExecutable,
		api.PackageFileKindOther,
	}
	if disc {
		extraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:          spEvidencePolicy("sp_package_safety"),
			BlockArchives:     true,
			BlockedExtraKinds: extraKinds,
		},
	)...)
	if subject.TVPack {
		failures = append(failures, trackers.ValidateCompleteEpisodeRange(
			subject.PackageFacts,
			trackers.EpisodeRangePolicy{
				Evidence: spEvidencePolicy("sp_pack_completeness"),
				Season:   subject.SeasonInt,
			},
		)...)
		failures = append(failures, trackers.ValidatePerFileUniformity(
			subject.MediaFileFacts,
			trackers.PerFileUniformityPolicy{
				Evidence: spEvidencePolicy("sp_pack_uniformity"),
				Fields: []trackers.MediaUniformityField{
					trackers.MediaUniformityFieldSource,
					trackers.MediaUniformityFieldResolution,
					trackers.MediaUniformityFieldVideoCodec,
					trackers.MediaUniformityFieldVideoEncode,
					trackers.MediaUniformityFieldAudioLanguages,
					trackers.MediaUniformityFieldSubtitleLanguages,
				},
			},
		)...)
	}
	failures = append(failures, spSoftwareFailures(subject)...)
	failures = append(failures, spAdultContentFailures(subject)...)
	failures = append(failures, spDiscStructureFailures(subject)...)
	failures = append(failures, spResolutionFailures(subject)...)
	return failures, nil
}

func spEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func spSoftwareFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	typeValue := strings.ToUpper(strings.TrimSpace(subject.Type))
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(subject.Release.Type))
	}
	switch typeValue {
	case "APP", "APPLICATION", "SOFTWARE":
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"sp_block_software",
			"software uploads are not allowed",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusComplete,
		)}
	default:
		return nil
	}
}

func spAdultContentFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	status := subject.ProvenanceFacts.Status
	ruleSubject := trackers.RuleSubjectFromValidation(subject)
	genres := trackers.RuleGenres(ruleSubject)
	if len(genres) > 0 && status == api.MetadataEvidenceStatusUnavailable {
		status = api.MetadataEvidenceStatusPartial
	}
	if trackers.AdultContent(ruleSubject) {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"sp_block_adult",
			"pornographic content is not allowed",
			api.RuleDispositionStrict,
			status,
		)}
	}
	if status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"sp_content_classification_evidence",
			"complete current content-classification evidence is required",
			api.RuleDispositionAdvisory,
			status,
		)}
	}
	return nil
}

func spDiscStructureFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !unit3d.IsDiscType(subject.DiscType) {
		return nil
	}
	if subject.PackageFacts.Status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"sp_full_disc_structure",
			"complete full-disc file evidence is required",
			api.RuleDispositionAdvisory,
			subject.PackageFacts.Status,
		)}
	}
	if len(subject.FileList) == 1 && strings.EqualFold(filepath.Ext(subject.FileList[0]), ".iso") {
		return nil
	}
	paths := make([]string, 0, len(subject.FileList))
	for _, file := range subject.FileList {
		paths = append(paths, strings.ToUpper(filepath.ToSlash(filepath.Clean(file))))
	}
	valid := false
	switch strings.ToUpper(strings.TrimSpace(subject.DiscType)) {
	case "DVD":
		valid = spHasDiscPath(paths, "/VIDEO_TS/VIDEO_TS.IFO") && spHasDiscExtension(paths, ".VOB")
	case "BDMV":
		valid = spHasDiscPath(paths, "/BDMV/INDEX.BDMV") && spHasDiscPathFragment(paths, "/BDMV/STREAM/", ".M2TS")
	case "HDDVD":
		valid = spHasDiscPath(paths, "/HVDVD_TS/HV000I01.IFO") && spHasDiscExtension(paths, ".EVO")
	}
	if valid {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"sp_full_disc_structure",
		"full-disc package does not contain the required source structure",
		api.RuleDispositionStrict,
		subject.PackageFacts.Status,
	)}
}

func spHasDiscPath(paths []string, suffix string) bool {
	relativeSuffix := strings.TrimPrefix(suffix, "/")
	for _, file := range paths {
		normalized := filepath.ToSlash(filepath.Clean(file))
		if normalized == relativeSuffix || strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func spHasDiscExtension(paths []string, extension string) bool {
	for _, file := range paths {
		if strings.EqualFold(filepath.Ext(file), extension) {
			return true
		}
	}
	return false
}

func spHasDiscPathFragment(paths []string, fragment string, extension string) bool {
	relativeFragment := strings.TrimPrefix(fragment, "/")
	for _, file := range paths {
		normalized := filepath.ToSlash(filepath.Clean(file))
		hasFragment := strings.HasPrefix(normalized, relativeFragment) || strings.Contains(normalized, fragment)
		if hasFragment && strings.EqualFold(filepath.Ext(file), extension) {
			return true
		}
	}
	return false
}

func spResolutionFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	ruleSubject := unit3d.ValidationRuleSubject(subject)
	resolution := unit3d.RuleResolution(ruleSubject)
	if resolution == "" {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"sp_resolution_evidence",
			"known resolution evidence is required",
			api.RuleDispositionAdvisory,
			subject.MediaFileFacts.TechnicalStatus,
		)}
	}
	if !unit3d.ResolutionBelow(resolution, "1080p") || subject.Anime ||
		strings.EqualFold(strings.TrimSpace(subject.DiscType), "DVD") {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"sp_lower_resolution_availability",
		"sub-1080p release requires manual proof that no higher-resolution source is available",
		api.RuleDispositionAdvisory,
		subject.AvailabilityFacts.Status,
	)}
}
