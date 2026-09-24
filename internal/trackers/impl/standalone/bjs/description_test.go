// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestBuildDescriptionReplacesImportedSignatures(t *testing.T) {
	const imported = "[quote=Release notes]\n[b]Preserve this BBCode[/b]\n[/quote]\n\n[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]\n\n[right]Created by Upload Assistant[/right]"

	got := buildDescription(trackers.PreparationInput{}, trackers.DescriptionAssets{Description: imported})
	for _, preserved := range []string{"[quote=Release notes]", "[b]Preserve this BBCode[/b]"} {
		if !strings.Contains(got, preserved) {
			t.Fatalf("expected %q to be preserved, got %q", preserved, got)
		}
	}
	for _, stale := range []string{"Uploaded by upbrr", "Upload Assistant"} {
		if strings.Contains(got, stale) {
			t.Fatalf("expected imported footer %q to be removed, got %q", stale, got)
		}
	}
	if strings.Count(got, "Upload realizado via upbrr") != 1 {
		t.Fatalf("expected one BJS footer, got %q", got)
	}

	got = buildDescription(trackers.PreparationInput{}, trackers.DescriptionAssets{Description: imported, Final: true})
	if got != imported {
		t.Fatalf("expected final description to remain unchanged, got %q", got)
	}
}
