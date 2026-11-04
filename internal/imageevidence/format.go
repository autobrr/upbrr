// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imageevidence

import (
	"bytes"
	"crypto/sha256"
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
	formatEvidenceCacheSize = 256
)

var (
	formatDecodeMu  sync.Mutex
	verifiedFormats = make(map[[sha256.Size]byte]string)
)

// VerifiedFormat verifies bounded local image bytes, reusing a prior full decode
// only for identical content. Cache misses decode one image at a time.
// Missing, unreadable, malformed, or over-budget content has unknown format;
// callers must not interpret that result as rejecting the image reference.
func VerifiedFormat(path string) string {
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
	return VerifiedFormatBytes(payload)
}

// VerifiedFormatBytes fully decodes bounded image content once while its digest
// remains in the bounded process-local cache. Invalid content has unknown format.
// File readers rehash current bytes, so replacement cannot reuse stale evidence.
func VerifiedFormatBytes(payload []byte) string {
	if len(payload) == 0 || len(payload) > formatEvidenceMaxBytes {
		return ""
	}
	digest := sha256.Sum256(payload)
	formatDecodeMu.Lock()
	defer formatDecodeMu.Unlock()
	if format, ok := verifiedFormats[digest]; ok {
		return format
	}
	format := decodeFormat(payload)
	if len(verifiedFormats) >= formatEvidenceCacheSize {
		clear(verifiedFormats)
	}
	verifiedFormats[digest] = format
	return format
}

func decodeFormat(payload []byte) string {
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
