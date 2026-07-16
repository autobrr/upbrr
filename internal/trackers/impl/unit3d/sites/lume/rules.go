// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
		Language: &ruletypes.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonDisc: true,
		},
		ExtraCheck: checkRequirements,
	}
}

func checkRequirements(ctx context.Context, meta api.RuleSubject, _ api.Logger) ruletypes.Result {
	if err := ctx.Err(); err != nil {
		return ruletypes.Fail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if !unit3d.IsDiscType(meta.DiscType) && !strings.EqualFold(strings.TrimSpace(meta.Container), "mkv") {
		return ruletypes.Fail("LUME only allows MKV containers for non-disc uploads.")
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return ruletypes.Pass()
	}
	resolution := unit3d.RuleResolution(meta)
	if resolution == "" {
		return ruletypes.Fail("LUME requires a known resolution")
	}
	if unit3d.ResolutionBelow(resolution, "720p") {
		return ruletypes.Fail("LUME only allows SD releases when the content does not have a higher resolution release.")
	}
	return ruletypes.Pass()
}
