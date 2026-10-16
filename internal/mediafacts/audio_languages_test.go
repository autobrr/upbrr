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

func TestAudioLanguagesFromMediaInfoTextStopsAtMenu(t *testing.T) {
	t.Parallel()
	value := "Audio #1\nFormat : AAC\n\nMenu\nLanguage : pt-BR\n"
	if got := AudioLanguagesFromMediaInfoText(value); len(got) != 0 {
		t.Fatalf("audio languages = %v, want no audio language", got)
	}
}

func TestAudioLanguagesFromMediaInfoTextWithCoverage(t *testing.T) {
	t.Parallel()

	trackWithAllFields := "Audio #1\nLanguage : en\nFormat : E-AC-3\nChannel(s) : 6 channels\n"
	trackMissingLanguage := "Audio #1\nFormat : AAC\nChannel(s) : 2 channels\n"
	multiTrackPartial := trackWithAllFields + "\n" + trackMissingLanguage

	// All tracks declare the field: complete.
	values, complete := AudioLanguagesFromMediaInfoTextWithCoverage(trackWithAllFields)
	if !complete || !slices.Equal(values, []string{"en"}) {
		t.Fatalf("audio languages = %v, complete=%v, want [en] complete=true", values, complete)
	}

	// Track lacks the field: partial.
	values, complete = AudioLanguagesFromMediaInfoTextWithCoverage(trackMissingLanguage)
	if complete || len(values) != 0 {
		t.Fatalf("audio languages = %v, complete=%v, want [] complete=false", values, complete)
	}

	// Mixed: one track has language, one does not: partial.
	values, complete = AudioLanguagesFromMediaInfoTextWithCoverage(multiTrackPartial)
	if complete || !slices.Equal(values, []string{"en"}) {
		t.Fatalf("audio languages = %v, complete=%v, want [en] complete=false", values, complete)
	}

	// No audio tracks: complete (vacuously).
	values, complete = AudioLanguagesFromMediaInfoTextWithCoverage("General\nFormat : Matroska\n")
	if !complete || len(values) != 0 {
		t.Fatalf("audio languages = %v, complete=%v, want [] complete=true", values, complete)
	}
}
