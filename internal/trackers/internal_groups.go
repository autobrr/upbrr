// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

var unusableTrackerReleaseGroups = []string{"mixed", "nogroup", "nogrp", "unk", "unknown"}

// GroupPolicyDecision is the tracker-local result of applying configured group lists.
type GroupPolicyDecision struct {
	Group           string
	PersonalRelease bool
	Internal        bool
	SameGroupOnly   bool
}

// ResolveGroupPolicy applies one tracker's personal, internal, and duplicate
// bypass lists to an upload subject. An explicit personal-release choice wins
// over the configured personal group default.
func ResolveGroupPolicy(trackerCfg config.TrackerConfig, meta api.UploadSubject) GroupPolicyDecision {
	group := NormalizeTrackerReleaseGroup(meta.Tag)
	decision := GroupPolicyDecision{
		Group:           group,
		PersonalRelease: meta.PersonalRelease,
	}
	if meta.PersonalReleaseOverride != nil {
		decision.PersonalRelease = *meta.PersonalReleaseOverride
	} else if trackerGroupListContains(trackerCfg.PersonalReleaseGroups, group) {
		decision.PersonalRelease = true
	}
	decision.Internal = trackerGroupListContains(trackerCfg.InternalGroups, group)
	decision.SameGroupOnly = decision.Internal || trackerGroupListContains(trackerCfg.DupeBypassGroups, group)
	return decision
}

// ApplyGroupPolicy returns a tracker-local upload subject and its resolved group policy.
func ApplyGroupPolicy(trackerCfg config.TrackerConfig, meta api.UploadSubject) (api.UploadSubject, GroupPolicyDecision) {
	decision := ResolveGroupPolicy(trackerCfg, meta)
	meta.PersonalRelease = decision.PersonalRelease
	return meta, decision
}

// TrackerGroupRestriction reports the normalized group used to suppress
// confirmed different-group duplicate candidates for one tracker.
func TrackerGroupRestriction(trackerCfg config.TrackerConfig, tag string) (string, bool) {
	group := NormalizeTrackerReleaseGroup(tag)
	return group, trackerGroupListContains(trackerCfg.DupeBypassGroups, group) || trackerGroupListContains(trackerCfg.InternalGroups, group)
}

// NormalizeTrackerReleaseGroup removes one conventional leading hyphen and
// rejects parser sentinels that do not identify a single release group.
func NormalizeTrackerReleaseGroup(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "-")
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(unusableTrackerReleaseGroups, strings.ToLower(value)) ||
		strings.ContainsAny(value, ",/\\|&+") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return ""
	}
	return value
}

func trackerGroupListContains(values config.CSVList, group string) bool {
	if group == "" {
		return false
	}
	return slices.ContainsFunc(values, func(value string) bool {
		return strings.EqualFold(NormalizeTrackerReleaseGroup(value), group)
	})
}

// IsInternalGroup reports whether meta's release group is configured as internal for tracker.
func IsInternalGroup(cfg config.Config, tracker string, meta api.UploadSubject) bool {
	return ResolveGroupPolicy(trackerConfigFor(cfg, tracker), meta).Internal
}
