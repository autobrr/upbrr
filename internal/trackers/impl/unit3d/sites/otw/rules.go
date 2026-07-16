// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *ruletypes.RuleSet { return &ruletypes.RuleSet{ExtraCheck: checkGenres} }

func checkGenres(ctx context.Context, meta api.RuleSubject, _ api.Logger) ruletypes.Result {
	if err := ctx.Err(); err != nil {
		return ruletypes.Fail(fmt.Errorf("context canceled: %w", err).Error())
	}
	genres := unit3d.RuleGenres(meta)
	if !unit3d.ContainsRuleValue(genres, []string{"animation", "family"}) {
		return ruletypes.Fail("Genre does not match Animation or Family for OTW.")
	}
	if unit3d.AdultContent(meta) {
		return ruletypes.Fail("Adult animation not allowed at OTW.")
	}
	if unit3d.ContainsRuleValue(genres, []string{"reality", "game show", "game-show", "reality tv", "reality television"}) {
		return ruletypes.Fail("Reality / Game Show content not allowed at OTW.")
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
			return ruletypes.Fail(fmt.Sprintf("Group %s is only allowed for raw type content at OTW", group))
		}
	}
	return ruletypes.Pass()
}
