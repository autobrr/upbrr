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
		Check:                 checkRequirements,
	}
}

func checkRequirements(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX", "ENCODE", "WEBDL", "WEBRIP":
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"container",
				fmt.Sprintf(
					"Container %q is not allowed for %s. Only MKV and MP4 are permitted.",
					meta.Container,
					strings.ToUpper(strings.TrimSpace(meta.Type)),
				),
				api.RuleDispositionStrict,
			)}, nil
		}
	}
	return nil, nil
}
