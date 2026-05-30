// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows

package pathutil

import "path/filepath"

func resolveExistingPath(value string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", false
	}
	return resolved, true
}
