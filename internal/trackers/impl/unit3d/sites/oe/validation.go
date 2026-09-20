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
	"github.com/autobrr/upbrr/pkg/api"
)

var oeLinkedScreenshotPattern = regexp.MustCompile(
	`(?is)\[url=https?://[^\]]+\]\s*\[img(?:=[^\]]+|\s+width=[^\]]+)?\]\s*(https?://[^\s\[]+)\s*\[/img\]\s*\[/url\]`,
)

func descriptionValidationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-oe-description-v1",
		Check: checkDescriptionRequirements,
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
