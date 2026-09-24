// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioAnalysisFallsBetweenMenusAndScreenshots(t *testing.T) {
	t.Parallel()
	got := buildDescription(trackers.PreparationInput{}, trackers.DescriptionAssets{
		Description: "Notes\n\n[spoiler=source_audio]\n[img]https://images.example.invalid/audio.png[/img]\n[/spoiler]",
		MenuImages:  []api.ScreenshotImage{{WebURL: "https://images.example.invalid/menu", RawURL: "https://images.example.invalid/menu.png"}},
		Screenshots: []api.ScreenshotImage{{WebURL: "https://images.example.invalid/shot", RawURL: "https://images.example.invalid/shot.png"}},
	})
	if strings.Index(got, "menu.png") >= strings.Index(got, "audio.png") || strings.Index(got, "audio.png") >= strings.Index(got, "shot.png") ||
		!strings.Contains(got, "[img=350]https://images.example.invalid/audio.png[/img]") {
		t.Fatalf("audio analysis placement = %q", got)
	}
}
