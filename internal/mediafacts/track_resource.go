// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// TrackResourceID preserves the inspected track inventory's lexical path identity.
// It must not clean or case-fold paths: existing prepared generations use this hash.
func TrackResourceID(path string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(path)))
	return "media_" + hex.EncodeToString(sum[:12])
}
