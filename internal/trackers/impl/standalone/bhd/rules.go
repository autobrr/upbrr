// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn/xxx is not allowed at BHD.",
	}
}

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "standalone-bhd-constructibility-v1",
		Check: checkRequirements,
	}
}

func checkRequirements(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 3)
	if _, ok := Source(meta.Source); !ok {
		failures = append(failures, trackers.NewRuleFailure(
			"unsupported_source",
			"BHD does not support the release source.",
			api.RuleDispositionStrict,
		))
	}
	if meta.Identity.TMDBID <= 0 {
		failures = append(failures, trackers.NewRuleFailure(
			"required_provider_id",
			"BHD requires a canonical TMDB ID.",
			api.RuleDispositionStrict,
		))
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX", "ENCODE", "WEBDL", "WEBRIP":
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			failures = append(failures, trackers.NewRuleFailure(
				"container",
				fmt.Sprintf(
					"Container %q is not allowed for %s. Only MKV and MP4 are permitted.",
					meta.Container,
					strings.ToUpper(strings.TrimSpace(meta.Type)),
				),
				api.RuleDispositionStrict,
			))
		}
	}
	return failures, nil
}
