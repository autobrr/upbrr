// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
)

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

// MediaBinding identifies media derived from this exact prepared generation.
func (r PreparedRelease) MediaBinding() (PreparedMediaBinding, error) {
	fingerprint, err := CanonicalWorkflowFingerprint(struct {
		ContractVersion            string
		SourceFingerprint          string
		FactInstructionFingerprint string
		Generation                 PreparedGeneration
		Discs                      []DiscItemFacts
		SelectedPlaylists          []PlaylistInfo
	}{
		ContractVersion:            r.Compatibility.ContractVersion,
		SourceFingerprint:          r.Compatibility.SourceFingerprint,
		FactInstructionFingerprint: r.Compatibility.FactInstructionFingerprint,
		Generation:                 r.Generation,
		Discs:                      r.Disc.Items,
		SelectedPlaylists:          r.Disc.SelectedPlaylists(),
	})
	if err != nil {
		return PreparedMediaBinding{}, fmt.Errorf("prepared release: fingerprint prepared media: %w", err)
	}
	binding := PreparedMediaBinding{
		SourcePath:               r.Source.SourcePath,
		PreparedMediaFingerprint: string(fingerprint),
		PreparedGeneration:       r.Generation,
	}
	if !binding.Valid() {
		return PreparedMediaBinding{}, errors.New("prepared release: incomplete media binding")
	}
	return binding, nil
}
