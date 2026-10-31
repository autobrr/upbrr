// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imageevidence

import (
	"fmt"
	"image"
	"strings"
)

// ResolutionHeight returns the height of a supported resolution label, or zero
// when no image-height constraint is known.
func ResolutionHeight(resolution string) int {
	return map[string]int{
		"8640p": 8640,
		"4320p": 4320,
		"2160p": 2160,
		"1440p": 1440,
		"1080p": 1080,
		"1080i": 1080,
		"720p":  720,
		"576p":  576,
		"576i":  576,
		"540p":  540,
		"540i":  540,
		"480p":  480,
		"480i":  480,
	}[strings.TrimSpace(resolution)]
}

// ResolutionMismatch returns a URL-free rejection reason, or an empty string
// when the resolution is unknown or the image meets the current tier. Images
// taller than a non-DVD tier use their shorter edge. DVDs retain physical height
// and permit 30% oversizing.
// Images below 70% of the expected height are rejected.
func ResolutionMismatch(config image.Config, expectedHeight int, isDVD bool) string {
	if expectedHeight <= 0 {
		return ""
	}
	actualHeight := config.Height
	if !isDVD && actualHeight > expectedHeight {
		actualHeight = min(config.Width, config.Height)
	}
	lowerBound := int(float64(expectedHeight) * 0.70)
	upperBound := expectedHeight
	if isDVD {
		upperBound = int(float64(expectedHeight) * 1.30)
	}
	if actualHeight < lowerBound || actualHeight > upperBound {
		return fmt.Sprintf("resolution_mismatch_actual_%d_expected_%d", actualHeight, expectedHeight)
	}
	return ""
}
