// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import "strings"

// IsDiscType recognizes Blu-ray, DVD, and HD DVD labels, ignoring case and separators.
func IsDiscType(value string) bool {
	normalized := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
	switch normalized {
	case "BDMV", "BLURAY", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

// IsFullDisc distinguishes complete discs from remuxes, including canonical
// DISC releases whose source-specific disc label is unavailable.
func IsFullDisc(discType, releaseType string) bool {
	if strings.EqualFold(strings.TrimSpace(releaseType), "REMUX") {
		return false
	}
	return IsDiscType(discType) || strings.EqualFold(strings.TrimSpace(releaseType), "DISC")
}
