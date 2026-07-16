// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package blu

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

func Rules() *ruletypes.RuleSet { return &ruletypes.RuleSet{ExtraCheck: checkContainer} }

func checkContainer(ctx context.Context, meta api.RuleSubject, _ api.Logger) ruletypes.Result {
	if err := ctx.Err(); err != nil {
		return ruletypes.Fail(fmt.Errorf("context canceled: %w", err).Error())
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return ruletypes.Pass()
	}
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	if container == "" {
		return ruletypes.Pass()
	}
	allowed := []string{"mkv"}
	typeValue := unit3d.RuleType(meta)
	if typeValue == "HDTV" {
		allowed = append(allowed, "ts")
	}
	if (typeValue == "WEBDL" || typeValue == "HDTV") && unit3d.DolbyVisionOnly(meta) {
		allowed = append(allowed, "mp4")
	}
	if unit3d.ContainsRuleValue([]string{container}, allowed) {
		return ruletypes.Pass()
	}
	return ruletypes.Fail("BLU requires one of the following containers for this release: " + strings.ToUpper(strings.Join(allowed, ", ")))
}
