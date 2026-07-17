// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ttr

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Language: &trackers.LanguageRule{
		Languages:    []string{"spanish", "es", "spa"},
		RequireAudio: true,
		RequireSubs:  true,
	}, ExtraCheck: checkSubtitleOnly}
}

func checkSubtitleOnly(ctx context.Context, meta api.RuleSubject, _ api.Logger) trackers.RuleResult {
	if err := ctx.Err(); err != nil {
		return trackers.RuleFail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if !unit3d.ContainsRuleValue(unit3d.NormalizeRuleValues(meta.Release.Language), []string{"spanish", "es", "spa"}) {
		return trackers.RuleFail("TTR requires at least one Spanish audio or subtitle track.")
	}
	return trackers.RulePass()
}
