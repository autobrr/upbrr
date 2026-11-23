// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// PackScreenshotMembers retains eligible prepared video members in source
// order. It never takes paths from screenshot controls or emits them publicly.
func PackScreenshotMembers(paths []string) []api.ScreenshotPackMember {
	var members []api.ScreenshotPackMember
	seen := make(map[string]bool)
	for _, path := range paths {
		path = strings.TrimSpace(path)
		name := strings.ToLower(pathing.Base(path))
		if !filesystem.IsVideoFile(name) || isPackSample(name) {
			continue
		}
		id := TrackResourceID(path)
		if !seen[id] {
			seen[id] = true
			members = append(members, api.ScreenshotPackMember{ID: id, Path: path})
		}
	}
	return members
}

func isPackSample(name string) bool {
	return slices.Contains(strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '!'
	}), "sample")
}
