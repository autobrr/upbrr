// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules strictly requires valid MediaInfo encode settings and enforces LUME's
// non-disc container, resolution, and language limits. Adult failures remain
// waivable; language failures are strict.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
		Language: &trackers.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonDisc: true,
		},
		Check: checkRequirements,
	}
}

func checkRequirements(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 2)
	if !unit3d.IsDiscType(meta.DiscType) && !strings.EqualFold(strings.TrimSpace(meta.Container), "mkv") {
		failures = append(failures, trackers.NewRuleFailure("container", "LUME only allows MKV containers for non-disc uploads.", api.RuleDispositionStrict))
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return failures, nil
	}
	resolution := unit3d.RuleResolution(meta)
	if resolution == "" {
		failures = append(failures, trackers.NewRuleFailure("resolution_required", "LUME requires a known resolution", api.RuleDispositionStrict))
		return failures, nil
	}
	if unit3d.ResolutionBelow(resolution, "720p") {
		failures = append(failures, trackers.NewRuleFailure(
			"min_resolution",
			"LUME only allows SD releases when the content does not have a higher resolution release.",
			api.RuleDispositionStrict,
		))
	}
	return failures, nil
}
