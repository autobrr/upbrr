// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux && !darwin && !windows

package webserver

import "context"

func browseVolumeKey(_ context.Context, _ string) string {
	return ""
}
