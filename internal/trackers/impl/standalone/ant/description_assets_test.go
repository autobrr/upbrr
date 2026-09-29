// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionPreservesAudioAfterMenus(t *testing.T) {
	t.Parallel()
	const audio = "[spoiler=source_audio]\n[img]https://images.example.invalid/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]"
	got := buildDescription(trackers.PreparationInput{}, trackers.DescriptionAssets{
		Description: "Notes\n\n" + audio,
		MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}},
	})
	for _, token := range []string{"Notes", "menu.png", "audio.png", "Peak: -1 dB"} {
		if !strings.Contains(got, token) {
			t.Fatalf("missing %q in %q", token, got)
		}
	}
	if strings.Index(got, "menu.png") >= strings.Index(got, "[spoiler=source_audio]") {
		t.Fatalf("audio precedes menu: %q", got)
	}
}

func TestBuildDescriptionUsesPreparedDiscMenuAssets(t *testing.T) {
	t.Parallel()

	assets := trackers.DescriptionAssets{
		Description: "Body token",
		MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}},
		Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/normal.png"}},
	}
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "ANT",
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{Description: config.DescriptionSettingsConfig{
			DiscMenuHeader: "Disc menu token",
		}}),
		Assets: &assets,
	})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	assertDescriptionTokensInOrder(t, result.Description, "Body token", "Disc menu token", "https://images.example.invalid/menu.png")
	if strings.Contains(result.Description, "normal.png") {
		t.Fatalf("normal screenshot leaked into ANT description: %q", result.Description)
	}

	final := trackers.DescriptionAssets{
		Description: " Authoritative final token ",
		Final:       true,
		MenuImages:  assets.MenuImages,
	}
	result, err = prepareDescription(context.Background(), trackers.PreparationInput{Tracker: "ANT", Assets: &final})
	if err != nil {
		t.Fatalf("build final description: %v", err)
	}
	if result.Description != "Authoritative final token" {
		t.Fatalf("final description = %q", result.Description)
	}
}

func TestBuildDescriptionLinksHostedDiscMenuThumbnail(t *testing.T) {
	t.Parallel()
	assets := trackers.DescriptionAssets{
		Description: "Body token",
		MenuImages: []api.ScreenshotImage{
			{
				ImgURL: "https://images.example.invalid/menu-thumb.png",
				RawURL: "https://images.example.invalid/menu-full.png",
				WebURL: "https://images.example.invalid/menu-page",
			},
			{
				ImgURL: "https://images.example.invalid/other-thumb.png",
				RawURL: "https://images.example.invalid/other-full.png",
			},
		},
		Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/normal.png"}},
	}
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{Tracker: "ANT", Assets: &assets})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	want := "[url=https://images.example.invalid/menu-page][img]https://images.example.invalid/menu-thumb.png[/img][/url]"
	wantDirect := "[url=https://images.example.invalid/other-full.png][img]https://images.example.invalid/other-thumb.png[/img][/url]"
	if !strings.Contains(result.Description, want) || !strings.Contains(result.Description, wantDirect) ||
		strings.Contains(result.Description, "[img]https://images.example.invalid/menu-full.png[/img]") ||
		strings.Contains(result.Description, "normal.png") {
		t.Fatalf("hosted menu preview = %q, want %q and %q without normal screenshot", result.Description, want, wantDirect)
	}
}

func assertDescriptionTokensInOrder(t *testing.T, description string, tokens ...string) {
	t.Helper()
	previous := -1
	for _, token := range tokens {
		position := strings.Index(description, token)
		if position <= previous {
			t.Fatalf("description tokens out of order at %q: %q", token, description)
		}
		previous = position
	}
}
