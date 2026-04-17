// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// CloneDescriptionBuilderGroups deep-copies the reference-typed fields used by
// DescriptionBuilderGroup so callers can safely reuse cached or queued values.
func CloneDescriptionBuilderGroups(groups []DescriptionBuilderGroup) []DescriptionBuilderGroup {
	if len(groups) == 0 {
		return nil
	}
	cloned := make([]DescriptionBuilderGroup, len(groups))
	for idx, group := range groups {
		cloned[idx] = group
		cloned[idx].Trackers = append([]string(nil), group.Trackers...)
		cloned[idx].ImageHost.AllowedHosts = append([]string(nil), group.ImageHost.AllowedHosts...)
	}
	return cloned
}
