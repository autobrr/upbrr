// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import "github.com/autobrr/upbrr/internal/bbcode"

// CleanDescription removes PTP-specific description markup and returns the
// cleaned text together with extracted non-comparison images.
func CleanDescription(description string, discType string) bbcode.Report {
	return bbcode.CleanPTPDescription(description, discType)
}
