// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *trackers.RuleSet { return &trackers.RuleSet{Check: checkGenres} }

func checkGenres(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 3)
	genres := unit3d.RuleGenres(meta)
	if !unit3d.ContainsRuleValue(genres, []string{"animation", "family"}) {
		failures = append(failures, trackers.NewRuleFailure("genre", "Genre does not match Animation or Family for OTW.", api.RuleDispositionWaivable))
	}
	if unit3d.AdultContent(meta) {
		failures = append(failures, trackers.NewRuleFailure("block_adult", "Adult animation not allowed at OTW.", api.RuleDispositionWaivable))
	}
	if unit3d.ContainsRuleValue(genres, []string{"reality", "game show", "game-show", "reality tv", "reality television"}) {
		failures = append(failures, trackers.NewRuleFailure("block_reality", "Reality / Game Show content not allowed at OTW.", api.RuleDispositionWaivable))
	}
	typeValue := unit3d.RuleType(meta)
	group := unit3d.RuleGroup(meta)
	if group != "" && typeValue != "WEBDL" && !unit3d.IsDiscType(meta.DiscType) {
		restricted := map[string]bool{
			"CMRG":     true,
			"EVO":      true,
			"TERMINAL": true,
			"VISION":   true,
		}
		if restricted[strings.ToUpper(group)] {
			failures = append(failures, trackers.NewRuleFailure(
				"group_type",
				fmt.Sprintf("Group %s is only allowed for raw type content at OTW", group),
				api.RuleDispositionWaivable,
			))
		}
	}
	return failures, nil
}
