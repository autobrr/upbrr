// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package blu

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules strictly limits non-disc uploads to MKV, with TS allowed for HDTV and
// MP4 allowed for Dolby Vision-only WEBDL or HDTV releases.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{Check: checkContainer} }

func checkContainer(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return nil, nil
	}
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	if container == "" {
		return nil, nil
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
		return nil, nil
	}
	return []api.RuleFailure{trackers.NewRuleFailure(
		"container",
		"BLU requires one of the following containers for this release: "+strings.ToUpper(strings.Join(allowed, ", ")),
		api.RuleDispositionStrict,
	)}, nil
}
