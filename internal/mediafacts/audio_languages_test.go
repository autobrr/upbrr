// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"
)

func TestAudioLanguagesFromMediaInfoText(t *testing.T) {
	t.Parallel()
	value := `
General
Format : Matroska

Audio #1
Format : E-AC-3
Language : en

Text #1
Language : pt-BR

Audio #2
Format : AAC
Language : pt-BR
`
	if got, want := AudioLanguagesFromMediaInfoText(value), []string{"en", "pt-BR"}; !slices.Equal(got, want) {
		t.Fatalf("audio languages = %v, want %v", got, want)
	}
}
