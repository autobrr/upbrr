// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestUnit3DDupeEntriesKeepMediaInfoAudioLanguages(t *testing.T) {
	t.Parallel()
	mediaInfo := "Audio #1\nLanguage : en\n\nAudio #2\nLanguage : pt-BR\n"

	search, _ := buildUnit3DSearchEntries([]unit3dSearchItem{{
		ID: json.Number("1"), Attributes: unit3dSearchAttrs{MediaInfo: mediaInfo},
	}}, 0, false)
	pending, _ := buildUnit3DPendingEntries([]unit3dPendingSearchItem{{
		ID: json.Number("2"), MediaInfo: mediaInfo,
	}}, unit3dSearchEndpoint{}, false)
	for _, entries := range [][]string{search[0].AudioLanguages, pending[0].AudioLanguages} {
		if !slices.Equal(entries, []string{"en", "pt-BR"}) {
			t.Fatalf("audio languages = %v", entries)
		}
	}
}
