// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "strings"

// PreparedMediaBinding identifies repository media for one exact prepared generation.
type PreparedMediaBinding struct {
	SourcePath               string
	PreparedMediaFingerprint string
	PreparedGeneration       PreparedGeneration
}

// Valid reports whether every binding component is present.
func (b PreparedMediaBinding) Valid() bool {
	return strings.TrimSpace(b.SourcePath) != "" &&
		strings.TrimSpace(b.PreparedMediaFingerprint) != "" &&
		b.PreparedGeneration > 0
}

// Equal reports exact equality across all binding components.
func (b PreparedMediaBinding) Equal(other PreparedMediaBinding) bool {
	return b.SourcePath == other.SourcePath &&
		b.PreparedMediaFingerprint == other.PreparedMediaFingerprint &&
		b.PreparedGeneration == other.PreparedGeneration
}
