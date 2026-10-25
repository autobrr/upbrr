// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imageevidence

import (
	"bytes"
	"image"
	_ "image/jpeg" // Register supported imported-image formats.
	_ "image/png"
	"io"
	"os"
	"sync"

	_ "golang.org/x/image/webp" // Register the existing imported WebP format.
)

const (
	formatEvidenceMaxBytes  = 20 * 1024 * 1024
	formatEvidenceMaxPixels = 35_389_440
)

var formatDecodeMu sync.Mutex

// VerifiedFormat fully decodes bounded local image bytes, one image at a time.
// Missing, unreadable, malformed, or over-budget content has unknown format;
// callers must not interpret that result as rejecting the image reference.
func VerifiedFormat(path string) string {
	formatDecodeMu.Lock()
	defer formatDecodeMu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > formatEvidenceMaxBytes {
		return ""
	}
	// Inspect and decode the same bounded bytes, even if the file changes meanwhile.
	payload, err := io.ReadAll(io.LimitReader(file, formatEvidenceMaxBytes+1))
	if err != nil || len(payload) > formatEvidenceMaxBytes {
		return ""
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil || !withinFormatPixelBudget(config.Width, config.Height) {
		return ""
	}
	_, format, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return ""
	}
	return format
}

func withinFormatPixelBudget(width, height int) bool {
	return width > 0 && height > 0 && width <= formatEvidenceMaxPixels/height
}
