// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package imageevidence verifies processing claims against local image bytes.
package imageevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// Digest identifies the current bytes, not the reusable pathname or extension.
func Digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("image evidence: open content: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("image evidence: hash content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Tonemapping retains a known processing claim only for the same local bytes.
// Missing, replaced, legacy, and externally supplied images remain unknown.
func Tonemapping(path, digest string, status api.ScreenshotTonemapping) api.ScreenshotTonemapping {
	if len(digest) != sha256.Size*2 || (status != api.ScreenshotTonemapped && status != api.ScreenshotNotTonemapped) {
		return ""
	}
	actual, err := Digest(path)
	if err != nil || !strings.EqualFold(actual, digest) {
		return ""
	}
	return status
}
