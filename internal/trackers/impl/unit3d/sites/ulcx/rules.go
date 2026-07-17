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
		Check: checkRules,
	}
}

func checkRules(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 3)
	if unit3d.ContainsRuleValue(unit3d.RuleKeywords(meta), []string{"concert"}) {
		failures = append(failures, trackers.NewRuleFailure("block_concert", "Concerts not allowed at ULCX.", api.RuleDispositionWaivable))
	}
	resolution := unit3d.RuleResolution(meta)
	if strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") && resolution != "2160p" && !unit3d.Animation(meta) && !unit3d.Anime(meta) {
		failures = append(failures, trackers.NewRuleFailure(
			"hevc_resolution_2160p",
			"This content might not fit HEVC rules for ULCX.",
			api.RuleDispositionStrict,
		))
	}
	typeValue := unit3d.RuleType(meta)
	if (typeValue == "ENCODE" || typeValue == "HDTV") && unit3d.ResolutionBelow(resolution, "720p") {
		failures = append(failures, trackers.NewRuleFailure(
			"encode_min_resolution",
			"Encodes must be at least 720p resolution for ULCX.",
			api.RuleDispositionStrict,
		))
	}
	if typeValue == "DVDRIP" {
		failures = append(failures, trackers.NewRuleFailure("block_dvdrip", "DVDRIPs are not allowed for ULCX.", api.RuleDispositionWaivable))
	}
	return failures, nil
}
