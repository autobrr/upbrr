// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import "github.com/autobrr/upbrr/internal/config"

// CaptureProcessConcurrency resolves the existing screenshot process policy for
// one batch. Image-host upload limits do not control decoder processes.
// With FFmpegLimit disabled, all pending captures may run concurrently; otherwise
// ProcessLimit is clamped to at least one and at most the pending count.
func CaptureProcessConcurrency(settings config.ScreenshotHandlingConfig, pending int) int {
	if pending <= 0 {
		return 0
	}
	if !settings.FFmpegLimit {
		return pending
	}
	return min(pending, max(1, settings.ProcessLimit))
}
