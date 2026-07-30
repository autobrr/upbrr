// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func finalizeDescription(description string, meta api.UploadSubject) string {
	if !strings.EqualFold(releaseGroup(meta), "island") {
		return description
	}
	const notes = "Release Shareisland 🏴‍☠️\nFalla girare, condividila e contribuisci a mantenerla viva restando in seed il più possi" + "bile.\nGrazie per il supporto!"
	trimmed := strings.TrimSpace(description)
	if strings.Contains(trimmed, notes) {
		return trimmed
	}
	if trimmed == "" {
		return notes
	}
	return trimmed + "\n\n" + notes
}

func releaseGroup(meta api.UploadSubject) string {
	for _, value := range []string{meta.Release.Group, meta.ArrReleaseGroup, strings.TrimPrefix(meta.Tag, "-")} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
