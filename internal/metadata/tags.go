// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"sort"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

// DetectTag returns the parsed release group as a hyphen-prefixed tag.
func DetectTag(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}

	group := strings.TrimSpace(ParseReleaseInfo(trimmed).Group)
	if group == "" {
		return ""
	}

	return "-" + group
}

// SeasonPackGroupTagInfo describes the parsed release groups found in a season
// pack file list.
type SeasonPackGroupTagInfo struct {
	// Mixed reports whether more than one usable release group was parsed.
	Mixed bool
	// Groups contains the distinct parsed group labels in stable display order.
	Groups []string
	// Notice contains the user-facing warning text for mixed season packs.
	Notice string
}

// DetectSeasonPackGroupTags parses file-list release groups and reports when a
// season pack contains more than one group tag.
func DetectSeasonPackGroupTags(meta preparationstate.State) SeasonPackGroupTagInfo {
	if !meta.TVPack || len(meta.FileList) < 2 {
		return SeasonPackGroupTagInfo{}
	}

	groupsByKey := make(map[string]string)
	for _, file := range meta.FileList {
		group := strings.TrimSpace(ParseReleaseInfo(file).Group)
		if group == "" {
			continue
		}
		key := normalizeSeasonPackGroupTag(group)
		if key == "" {
			continue
		}
		if _, exists := groupsByKey[key]; !exists {
			groupsByKey[key] = group
		}
	}
	if len(groupsByKey) < 2 {
		return SeasonPackGroupTagInfo{}
	}

	groups := make([]string, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.ToLower(groups[i]) < strings.ToLower(groups[j])
	})

	return SeasonPackGroupTagInfo{
		Mixed:  true,
		Groups: groups,
		Notice: fmt.Sprintf(
			"Season pack contains mixed group tags (%s); trackers with mixed-origin support will use Mixed.",
			strings.Join(groups, ", "),
		),
	}
}

// SeasonPackMixedGroupTagNotice returns the user-facing notice for season packs
// with mismatched file-level group tags.
func SeasonPackMixedGroupTagNotice(meta preparationstate.State) (string, bool) {
	info := DetectSeasonPackGroupTags(meta)
	if !info.Mixed || strings.TrimSpace(info.Notice) == "" {
		return "", false
	}
	return info.Notice, true
}

func normalizeSeasonPackGroupTag(group string) string {
	value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(group, "-")))
	switch value {
	case "", "nogrp", "nogroup", "unknown", "unk":
		return ""
	default:
		return value
	}
}
