// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
)

func TestExtractMediaInfoLanguagesSkipsCommentary(t *testing.T) {
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":    "Audio",
			"Language": "en",
			"Title":    "Director Commentary",
		},
		{"@type": "Audio", "Language": "fr"},
		{"@type": "Text", "Language": "en"},
	}
	audio, subs := extractMediaInfoLanguages(doc)
	if len(audio) != 1 || audio[0] != "French" {
		t.Fatalf("unexpected audio languages: %#v", audio)
	}
	if len(subs) != 1 || subs[0] != "English" {
		t.Fatalf("unexpected subtitle languages: %#v", subs)
	}
}

func TestExtractMediaInfoLanguagesSkipsCompatibility(t *testing.T) {
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":    "Audio",
			"Language": "en",
			"Title":    "Compatibility Track",
		},
		{"@type": "Audio", "Language": "ja"},
	}
	audio, _ := extractMediaInfoLanguages(doc)
	if len(audio) != 1 || audio[0] != "Japanese" {
		t.Fatalf("unexpected audio languages: %#v", audio)
	}
}

func TestExtractBDInfoLanguages(t *testing.T) {
	audio, subs := extractBDInfoLanguages(&discparse.BDInfo{
		Audio: []discparse.BDAudio{
			{Language: " English "},
			{Language: "French"},
			{Language: "English"},
			{Language: "Gaelic"},
			{Language: "Custom Dialect"},
		},
		Subtitles: []string{"Spanish", "spanish", "phi", "pt-BR"},
	})
	if !slices.Equal(audio, []string{"English", "French", "Scottish Gaelic", "Custom Dialect"}) {
		t.Fatalf("unexpected audio languages: %#v", audio)
	}
	if !slices.Equal(subs, []string{"Spanish", "Philippine languages", "Portuguese [pt-BR]"}) {
		t.Fatalf("unexpected subtitle languages: %#v", subs)
	}
}
