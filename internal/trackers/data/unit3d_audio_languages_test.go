// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestUnit3DDupeEntriesKeepMediaInfoAudioFacts(t *testing.T) {
	t.Parallel()
	mediaInfo := "Audio #1\nFormat : E-AC-3\nChannel(s) : 6 channels\nLanguage : en\n\nAudio #2\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : pt-BR\n"

	search, _ := buildUnit3DSearchEntries([]unit3dSearchItem{{
		ID: json.Number("1"), Attributes: unit3dSearchAttrs{MediaInfo: mediaInfo},
	}}, 0, false)
	pending, _ := buildUnit3DPendingEntries([]unit3dPendingSearchItem{{
		ID: json.Number("2"), MediaInfo: mediaInfo,
	}}, unit3dSearchEndpoint{}, false)
	for _, entry := range []struct {
		codecs, channels, languages []string
	}{
		{search[0].AudioCodecs, search[0].AudioChannels, search[0].AudioLanguages},
		{pending[0].AudioCodecs, pending[0].AudioChannels, pending[0].AudioLanguages},
	} {
		if !slices.Equal(entry.codecs, []string{"E-AC-3", "AAC"}) {
			t.Fatalf("audio codecs = %v", entry.codecs)
		}
		if !slices.Equal(entry.channels, []string{"6 channels", "2 channels"}) {
			t.Fatalf("audio channels = %v", entry.channels)
		}
		if !slices.Equal(entry.languages, []string{"en", "pt-BR"}) {
			t.Fatalf("audio languages = %v", entry.languages)
		}
	}
}
