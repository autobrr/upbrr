// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var oeLinkedScreenshotPattern = regexp.MustCompile(
	`(?is)\[url=https?://[^\]]+\]\s*\[img(?:=[^\]]+|\s+width=[^\]]+)?\]\s*(https?://[^\s\[]+)\s*\[/img\]\s*\[/url\]`,
)

// ValidationPolicy enforces OE's upload-guide and description requirements.
func ValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-oe-policy-v2",
		Check: checkRules,
	}
}

func checkRules(ctx context.Context, meta api.TrackerValidationSubject, logger api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	ruleSubject := unit3d.ValidationRuleSubject(meta)
	var failures []api.RuleFailure
	extraKinds := []api.PackageFileKind{
		api.PackageFileKindNFO,
		api.PackageFileKindImage,
		api.PackageFileKindText,
		api.PackageFileKindSample,
		api.PackageFileKindProof,
		api.PackageFileKindChecksum,
		api.PackageFileKindExecutable,
	}
	if !unit3d.IsDiscType(meta.DiscType) {
		extraKinds = append(extraKinds, api.PackageFileKindOther)
	}
	failures = append(failures, trackers.ValidatePackageExtensions(meta.PackageFacts, trackers.PackageExtensionPolicy{
		Evidence:          oeEvidencePolicy("oe_package_extras"),
		BlockArchives:     true,
		BlockedExtensions: []string{".avi"},
		BlockedExtraKinds: extraKinds,
	})...)
	if meta.TVPack || len(meta.PackageFacts.DetectedSeasons) > 1 {
		failures = append(failures, trackers.ValidateMultiSeasonPackage(meta.PackageFacts, oeEvidencePolicy("oe_multi_season_pack"))...)
	}
	if meta.TVPack && len(meta.PackageFacts.DetectedSeasons) <= 1 {
		failures = append(failures, trackers.NewRuleFailure(
			"oe_season_finished_airing",
			"OE permits full season packs only after the season has finished airing in your country; confirm it has finished.",
			api.RuleDispositionWaivable,
		))
	}
	if !unit3d.IsDiscType(meta.DiscType) {
		if strings.EqualFold(strings.TrimSpace(meta.Container), "avi") ||
			strings.Contains(strings.ToUpper(meta.VideoCodec), "XVID") || strings.Contains(strings.ToUpper(meta.VideoEncode), "XVID") {
			failures = append(failures, trackers.NewRuleFailure("oe_avi_xvid", "AVI and Xvid releases are not allowed at OE.", api.RuleDispositionStrict))
		}
	}
	if strings.Contains(strings.ToUpper(meta.Audio), "MP3") {
		failures = append(failures, trackers.NewRuleFailure("oe_mp3_audio", "MP3 audio is not allowed at OE.", api.RuleDispositionStrict))
	}
	if unit3d.ContainsRuleValue(unit3d.NormalizeRuleValues([]string{meta.Source, unit3d.RuleType(ruleSubject)}),
		[]string{"cam", "hdcam", "telesync", "hdts", "ts", "telecine", "tc", "screener", "dvdscr", "bdscr", "scr", "workprint"}) {
		failures = append(
			failures,
			trackers.NewRuleFailure("oe_preretail", "Camera recordings, screeners, and pre-retail content are not allowed at OE.", api.RuleDispositionStrict),
		)
	}
	group := unit3d.RuleGroup(ruleSubject)
	if strings.EqualFold(group, "iVy") {
		failures = append(failures, trackers.NewRuleFailure(
			"oe_ivy_availability", "iVy is allowed only when no other encodes exist on OE and is trumpable; confirm eligibility.", api.RuleDispositionWaivable,
		))
	}
	if group == "" || unit3d.IsNoGroupTag(group) {
		failures = append(failures, trackers.NewRuleFailure(
			"oe_nogrp_approval",
			"OE requires staff approval for NOGRP releases; confirm approval before uploading.",
			api.RuleDispositionWaivable,
		))
	}
	// The shared assessment covers ENCODE. WEBRip and DVDRip also require
	// settings; AV1 may instead supply settings in its description.
	switch unit3d.RuleType(ruleSubject) {
	case "WEBRIP", "DVDRIP":
		if normalizeCodec(meta.VideoCodec) != "AV1" && !meta.HasEncodeSettings {
			failures = append(failures, trackers.NewRuleFailure(
				"oe_encode_settings", "OE requires MediaInfo encode settings for re-encoded releases.", api.RuleDispositionStrict,
			))
		}
	}
	var requirements []trackers.AssetRequirement
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		requirements = append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindBDInfo})
	case "DVD":
		requirements = append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindDVDVOBMediaInfo})
	default:
		requirements = append(requirements, trackers.AssetRequirement{Kind: trackers.AssetKindMediaInfoText})
	}
	failures = append(failures, trackers.ValidateRequiredAssets(meta.AssetFacts, trackers.RequiredAssetPolicy{
		Evidence:     oeEvidencePolicy("oe_required_assets"),
		Requirements: requirements,
	})...)
	descriptionFailures, err := checkDescriptionRequirements(ctx, meta, logger)
	if err != nil {
		return nil, err
	}
	return append(failures, descriptionFailures...), nil
}

func oeEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}

func checkDescriptionRequirements(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	var failures []api.RuleFailure
	// Screenshot work happens after pre-dupe validation. Check the rendered
	// result only once it is final; the composer checks selected images earlier.
	if meta.DescriptionGroupsFinal {
		images := make(map[string]struct{})
		for _, match := range oeLinkedScreenshotPattern.FindAllStringSubmatch(meta.DescriptionOverride, -1) {
			images[match[1]] = struct{}{}
		}
		if len(images) < oeMinimumScreenshots {
			failures = append(failures, trackers.NewRuleFailure(
				"oe_description_screenshots", "OE requires at least three distinct linked screenshots in the final description", api.RuleDispositionStrict,
			))
		}
	}
	if oeRequiresEncodingSettings(meta.VideoCodec, meta.Type, meta.HasEncodeSettings) {
		failures = appendDescriptionRequirementFailure(
			failures,
			meta,
			oeEncodingSettingsKey,
			"oe_av1_encoding_settings",
			"OE requires AV1 encoding settings when MediaInfo does not provide them",
		)
	}
	if oeRequiresSourceNotes(meta.Tag) {
		failures = appendDescriptionRequirementFailure(
			failures,
			meta,
			oeSourceNotesKey,
			"oe_sm737_source_notes",
			"OE requires source notes for SM737 releases",
		)
	}
	return failures, nil
}

func appendDescriptionRequirementFailure(
	failures []api.RuleFailure,
	meta api.TrackerValidationSubject,
	key string,
	rule string,
	reason string,
) []api.RuleFailure {
	answer := strings.Join(strings.Fields(bbcode.NormalizeNewlines(meta.QuestionnaireAnswers[key])), " ")
	if answer == "" {
		return append(failures, trackers.NewRuleFailure(rule, reason, api.RuleDispositionStrict))
	}
	description := strings.Join(strings.Fields(bbcode.NormalizeNewlines(meta.DescriptionOverride)), " ")
	if meta.DescriptionGroupsFinal && !strings.Contains(description, answer) {
		return append(failures, trackers.NewRuleFailure(
			rule+"_description",
			reason+"; retain the supplied evidence in the final description or update the Input field",
			api.RuleDispositionStrict,
		))
	}
	return failures
}
