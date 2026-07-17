// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockDVDRip:           true,
		Language: &trackers.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			ApplyIfNonDisc: true,
		},
		ExtraCheck: checkRules,
	}
}

func checkRules(ctx context.Context, meta api.RuleSubject, _ api.Logger) trackers.RuleResult {
	if err := ctx.Err(); err != nil {
		return trackers.RuleFail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if unit3d.ContainsRuleValue(unit3d.RuleKeywords(meta), []string{"concert"}) {
		return trackers.RuleFail("Concerts not allowed at ULCX.")
	}
	resolution := unit3d.RuleResolution(meta)
	if strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") && resolution != "2160p" && !unit3d.Animation(meta) && !unit3d.Anime(meta) {
		return trackers.RuleFail("This content might not fit HEVC rules for ULCX.")
	}
	typeValue := unit3d.RuleType(meta)
	if (typeValue == "ENCODE" || typeValue == "HDTV") && unit3d.ResolutionBelow(resolution, "720p") {
		return trackers.RuleFail("Encodes must be at least 720p resolution for ULCX.")
	}
	if typeValue == "DVDRIP" {
		return trackers.RuleFail("DVDRIPs are not allowed for ULCX.")
	}
	return trackers.RulePass()
}
