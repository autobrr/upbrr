// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-mtv-constructibility-v2",
		Check: checkRequirements,
	}
}

// checkRequirements evaluates MTV's deterministic package, media, and asset
// constraints from prepared evidence without I/O or mutation.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	meta := standalone.UploadSubjectForValidation(subject)
	failures := make([]api.RuleFailure, 0, 14)
	if resolveCategoryID(meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_category",
			"release does not map to an MTV category",
			api.RuleDispositionStrict,
		))
	}
	if resolveSourceID(meta) == "0" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_source",
			"release does not map to an MTV source",
			api.RuleDispositionStrict,
		))
	}
	if resolveResolutionID(meta) == "10" {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_resolution",
			"release does not map to an MTV resolution",
			api.RuleDispositionStrict,
		))
	}
	blockedExtraKinds := []api.PackageFileKind{
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
	disc := trackers.IsDiscType(subject.DiscType)
	if disc {
		blockedExtraKinds = nil
	}
	failures = append(failures, trackers.ValidatePackageExtensions(subject.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:          mtvEvidencePolicy("mtv_package_safety"),
		BlockArchives:     true,
		BlockedExtraKinds: blockedExtraKinds,
	})...)
	if !disc && !subject.TVPack && !subject.Scene {
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			mtvEvidencePolicy("mtv_single_release_layout"),
		)...)
	}
	if subject.TVPack {
		failures = append(failures, trackers.ValidatePerFileUniformity(
			subject.MediaFileFacts,
			trackers.PerFileUniformityPolicy{
				Evidence: mtvEvidencePolicy("mtv_pack_uniformity"),
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
	failures = append(failures, mtvCodecFailures(subject)...)
	failures = append(failures, mtvGroupTokenFailures(subject)...)
	failures = append(failures, mtvTVDBTitleFailures(subject, meta)...)
	failures = append(failures, mtvAdultContentFailures(subject)...)
	return failures, nil
}

func mtvEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func mtvCodecFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	service := strings.ToUpper(strings.TrimSpace(subject.Service))
	serviceLongName := strings.ToUpper(strings.TrimSpace(subject.ServiceLongName))
	if resolveType(standalone.UploadSubjectForValidation(subject)) == "WEBDL" &&
		(service == "IT" || service == "ITUNES" || serviceLongName == "ITUNES") {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_itunes_webdl",
			"iTunes WEB-DL releases are not allowed",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusComplete,
		)}
	}
	codec := strings.ToUpper(strings.TrimSpace(subject.VideoCodec))
	if codec == "" {
		codec = strings.ToUpper(strings.TrimSpace(subject.VideoEncode))
	}
	if codec != "HEVC" && codec != "H.265" && codec != "X265" {
		return nil
	}
	resolution := strings.ToLower(strings.TrimSpace(subject.Release.Resolution))
	if resolution == "" {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_hevc_resolution",
			"resolution evidence is required for an HEVC release",
			api.RuleDispositionAdvisory,
			subject.MediaFileFacts.TechnicalStatus,
		)}
	}
	if resolution == "2160p" {
		return nil
	}
	group := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(subject.Tag, "-")))
	if subject.Anime && (resolution == "1080p" || resolution == "720p") && mtvAnimeHEVCGroups[group] {
		return nil
	}
	hdr := strings.ToUpper(strings.TrimSpace(subject.HDR))
	if !subject.Anime && resolution == "1080p" && (strings.Contains(hdr, "HDR") || strings.Contains(hdr, "DV")) {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_hevc_source_availability",
			"1080p live-action HEVC requires manual proof that no 2160p release exists",
			api.RuleDispositionAdvisory,
			subject.AvailabilityFacts.Status,
		)}
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"mtv_hevc_exception",
		"sub-2160p HEVC requires a saved group exception or staff approval",
		api.RuleDispositionWaivable,
		subject.MediaFileFacts.TechnicalStatus,
	)}
}

func mtvGroupTokenFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	facts := subject.MediaFileFacts
	if facts.Status != api.MetadataEvidenceStatusComplete ||
		facts.ExpectedFileCount <= 0 ||
		len(facts.Files) != facts.ExpectedFileCount {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_group_token",
			"complete per-file release-group evidence is required",
			api.RuleDispositionAdvisory,
			facts.Status,
		)}
	}
	groups := make(map[string]struct{}, len(facts.Files))
	for _, file := range facts.Files {
		group := mtvFileGroup(file.FileName)
		if group == "" {
			group = "NOGRP"
		}
		groups[group] = struct{}{}
	}
	expected := ""
	switch {
	case len(groups) == 1:
		for group := range groups {
			expected = group
		}
	case subject.Scene:
		expected = "SCENE"
	default:
		expected = "P2P"
	}
	actual := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(subject.Tag, "-")))
	switch actual {
	case "", "NOGROUP", "UNKNOWN", "UNK":
		actual = "NOGRP"
	}
	if strings.EqualFold(actual, expected) {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"mtv_group_token",
		fmt.Sprintf("release group token %q does not match proved package composition %q", actual, expected),
		api.RuleDispositionStrict,
		facts.Status,
	)}
}

func mtvFileGroup(fileName string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(fileName)), filepath.Ext(strings.TrimSpace(fileName)))
	index := strings.LastIndex(base, "-")
	if index < 0 || index == len(base)-1 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(base[index+1:]))
}

func mtvTVDBTitleFailures(subject api.TrackerValidationSubject, meta api.UploadSubject) []api.RuleFailure {
	if subject.Identity.Category != api.CanonicalCategoryTV {
		return nil
	}
	if matchingMTVTVDBMetadata(meta) && strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish) != "" {
		return nil
	}
	return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
		"mtv_tvdb_title",
		"current matching English TVDB title evidence is required",
		api.RuleDispositionStrict,
		subject.ProvenanceFacts.Status,
	)}
}

func mtvAdultContentFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	values := mtvClassificationValues(subject)
	status := subject.ProvenanceFacts.Status
	if len(values) > 0 && status == api.MetadataEvidenceStatusUnavailable {
		status = api.MetadataEvidenceStatusPartial
	}
	if slices.ContainsFunc(values, mtvAdultValue) {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_block_adult",
			"adult content is not allowed at MTV",
			api.RuleDispositionStrict,
			status,
		)}
	}
	if status != api.MetadataEvidenceStatusComplete {
		return []api.RuleFailure{trackers.NewEvidenceRuleFailure(
			"mtv_content_classification_evidence",
			"complete current content-classification evidence is required",
			api.RuleDispositionAdvisory,
			status,
		)}
	}
	return nil
}

func mtvClassificationValues(subject api.TrackerValidationSubject) []string {
	values := []string{subject.Release.Genre}
	if !subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return values
	}
	if metadata := subject.ProviderMetadata.TMDB; metadata != nil {
		values = append(values, metadata.Genres, metadata.Keywords)
	}
	if metadata := subject.ProviderMetadata.IMDB; metadata != nil {
		values = append(values, metadata.Genres)
	}
	if metadata := subject.ProviderMetadata.TVDB; metadata != nil {
		values = append(values, metadata.Genres)
	}
	return values
}

func mtvAdultValue(value string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	}) {
		switch token {
		case "adult", "erotic", "hentai", "porn", "pornography", "xxx":
			return true
		}
	}
	return false
}

var mtvAnimeHEVCGroups = map[string]bool{
	"AC":           true,
	"AERGIA":       true,
	"ARC":          true,
	"ARID":         true,
	"BAWS":         true,
	"CHIHIRO":      true,
	"COMMIE":       true,
	"CROW":         true,
	"CSS":          true,
	"DAE":          true,
	"DATTE13":      true,
	"DRAG":         true,
	"FLE":          true,
	"GJM":          true,
	"GJM-KALEIDO":  true,
	"HCHCSEN":      true,
	"IKAOS":        true,
	"JYSZE":        true,
	"KALEIDO-SUBS": true,
	"LEGION":       true,
	"LOSTYEARS":    true,
	"LYS1TH3A":     true,
	"MTBB":         true,
	"NETARO":       true,
	"NOYR":         true,
	"OKAY-SUBS":    true,
	"OZR":          true,
	"REZA":         true,
	"SAM":          true,
	"SHOWTHIGHS":   true,
	"SPIRALE":      true,
	"TTGA":         true,
	"UDF":          true,
	"UQW":          true,
	"VANILLA":      true,
	"VODES":        true,
	"WSE":          true,
}
