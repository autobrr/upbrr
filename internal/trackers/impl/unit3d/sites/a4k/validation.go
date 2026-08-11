// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package a4k

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var webRipRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])web-?rip([^[:alnum:]]|$)`)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID:    "unit3d-a4k-v1",
		Check: checkRequirements,
	}
}

// checkRequirements validates only projected facts. MediaInfo bitrate checks
// belong in preparation evidence, not in this side-effect-free callback.
func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	if isWebRip(subject) {
		return []api.RuleFailure{trackers.NewRuleFailure("a4k_webrip", "WEBRips are not allowed at A4K.", api.RuleDispositionStrict)}, nil
	}
	if resolution := unit3d.RuleResolution(unit3d.ValidationRuleSubject(subject)); resolution != "2160p" {
		return []api.RuleFailure{trackers.NewRuleFailure("a4k_resolution", "A4K only accepts 2160p releases.", api.RuleDispositionStrict)}, nil
	}
	if strings.TrimSpace(subject.DiscType) == "" {
		bitrate := subject.Assessments.VideoBitrate
		if bitrate.Status != api.VideoBitrateStatusPresent {
			return []api.RuleFailure{
				trackers.NewRuleFailure("a4k_video_bitrate", "A4K requires a valid prepared MediaInfo video bitrate.", api.RuleDispositionStrict),
			}, nil
		}
		minimum := int64(10_000_000)
		if strings.EqualFold(strings.TrimSpace(string(subject.Identity.Category)), "tv") {
			minimum = 6_000_000
		}
		if bitrate.BitsPerSecond < minimum {
			return []api.RuleFailure{trackers.NewRuleFailure("a4k_video_bitrate", "Video bitrate is below A4K's minimum.", api.RuleDispositionStrict)}, nil
		}
	}
	return nil, nil
}

func isWebRip(subject api.TrackerValidationSubject) bool {
	return strings.EqualFold(strings.TrimSpace(subject.Type), "WEBRIP") ||
		webRipRegex.MatchString(subject.Source) ||
		webRipRegex.MatchString(subject.Release.Source) ||
		webRipRegex.MatchString(subject.ReleaseName) ||
		webRipRegex.MatchString(subject.ReleaseNameNoTag)
}
