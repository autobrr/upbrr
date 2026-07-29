// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// checkRequirements enforces OTW-specific metadata and package requirements.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, logger api.Logger) ([]api.RuleFailure, error) {
	failures, err := checkGenres(ctx, subject, logger)
	if err != nil {
		return nil, err
	}
	meta := otwUploadSubject(subject)
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
	disc := isOTWCompleteDisc(meta)
	if disc {
		extraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:          otwEvidencePolicy("otw_package_safety"),
			BlockArchives:     true,
			BlockedExtraKinds: extraKinds,
		},
	)...)
	if !disc {
		failures = append(failures, trackers.ValidateMultiSeasonPackage(
			subject.PackageFacts,
			otwEvidencePolicy("otw_multi_season_non_disc"),
		)...)
		failures = append(failures, otwSeasonStructureFailures(subject)...)
	}
	if typeID(meta) == "" {
		failures = append(failures, trackers.NewRuleFailure(
			"otw_exact_type",
			"release type does not map to an OTW type",
			api.RuleDispositionStrict,
		))
	}
	failures = append(failures, otwNamingFailures(subject, meta)...)
	if subject.ProvenanceFacts.Status != api.MetadataEvidenceStatusComplete {
		failures = append(failures, trackers.NewEvidenceRuleFailure(
			"otw_content_classification_evidence",
			"complete current genre and content-classification evidence is required",
			api.RuleDispositionAdvisory,
			subject.ProvenanceFacts.Status,
		))
	}
	return failures, nil
}

func otwEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func otwSeasonStructureFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if subject.Identity.Category != api.CanonicalCategoryTV {
		return nil
	}
	if subject.PackageFacts.Status != api.MetadataEvidenceStatusComplete || len(subject.PackageFacts.DetectedSeasons) == 0 {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"otw_season_structure",
			"complete local season evidence is required",
			api.RuleDispositionAdvisory,
			subject.PackageFacts.Status,
		)}
	}
	if subject.SeasonInt < 0 {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"otw_season_structure",
			"canonical season metadata is invalid",
			api.RuleDispositionStrict,
			subject.PackageFacts.Status,
		)}
	}
	if subject.SeasonInt > 0 && !slices.Equal(subject.PackageFacts.DetectedSeasons, []int{subject.SeasonInt}) {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"otw_season_structure",
			"package season does not match canonical season metadata",
			api.RuleDispositionStrict,
			subject.PackageFacts.Status,
		)}
	}
	if subject.SeasonInt == 0 && !slices.Equal(subject.PackageFacts.DetectedSeasons, []int{0}) {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"otw_specials_structure",
			"extras and specials must use an S00 package",
			api.RuleDispositionStrict,
			subject.PackageFacts.Status,
		)}
	}
	return nil
}

func otwNamingFailures(subject api.TrackerValidationSubject, meta api.UploadSubject) []api.RuleFailure {
	title, year := currentOTWTMDBTitleYear(meta)
	if title != "" && year > 0 {
		return nil
	}
	ruleSubject := unit3d.ValidationRuleSubject(subject)
	if trackers.MetadataFieldPresent(trackers.MetadataFieldTMDBUnavailable, ruleSubject) &&
		strings.TrimSpace(subject.Release.Title) != "" &&
		subject.Release.Year > 0 {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"otw_naming_metadata",
		"current English title and original premiere year evidence is required",
		api.RuleDispositionStrict,
		subject.ProvenanceFacts.Status,
	)}
}

func otwUploadSubject(subject api.TrackerValidationSubject) api.UploadSubject {
	return api.UploadSubject{
		SourcePath:       subject.SourcePath,
		FileList:         append([]string(nil), subject.FileList...),
		DiscType:         subject.DiscType,
		Release:          subject.Release,
		ReleaseName:      subject.ReleaseName,
		ReleaseNameNoTag: subject.ReleaseNameNoTag,
		Tag:              subject.Tag,
		Identity:         subject.Identity,
		ProviderMetadata: subject.ProviderMetadata,
		SeasonInt:        subject.SeasonInt,
		EpisodeInt:       subject.EpisodeInt,
		SeasonStr:        subject.SeasonStr,
		EpisodeStr:       subject.EpisodeStr,
		TVPack:           subject.TVPack,
		DailyEpisodeDate: subject.DailyEpisodeDate,
		EpisodeTitle:     subject.EpisodeTitle,
		Disc:             subject.Disc,
		Type:             subject.Type,
		Source:           subject.Source,
		Audio:            subject.Audio,
		Channels:         subject.Channels,
		Is3D:             subject.Is3D,
		VideoCodec:       subject.VideoCodec,
		VideoEncode:      subject.VideoEncode,
		HDR:              subject.HDR,
		Distributor:      subject.Distributor,
		Region:           subject.Region,
		Repack:           subject.Repack,
		Service:          subject.Service,
	}
}
