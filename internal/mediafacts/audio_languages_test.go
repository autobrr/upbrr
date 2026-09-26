// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"
)

// TestAudioLanguagesFromMediaInfoText verifies all audio facts extracted from MediaInfo.
func TestAudioLanguagesFromMediaInfoText(t *testing.T) {
	t.Parallel()
	value := `
General
Format : Matroska

Audio #1
Format : E-AC-3
Channel(s) : 6 channels
Language : en

Text #1
Language : pt-BR

Audio #2
Format : AAC
Channel(s) : 2 channels
Language : pt-BR
`
	if got, want := AudioLanguagesFromMediaInfoText(value), []string{"en", "pt-BR"}; !slices.Equal(got, want) {
		t.Fatalf("audio languages = %v, want %v", got, want)
	}
	if got, want := AudioCodecsFromMediaInfoText(value), []string{"E-AC-3", "AAC"}; !slices.Equal(got, want) {
		t.Fatalf("audio codecs = %v, want %v", got, want)
	}
	if got, want := AudioChannelsFromMediaInfoText(value), []string{"6 channels", "2 channels"}; !slices.Equal(got, want) {
		t.Fatalf("audio channels = %v, want %v", got, want)
	}
}
