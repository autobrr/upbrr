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

func TestBuildDescriptionUsesPreparedDiscMenuAssets(t *testing.T) {
	t.Parallel()

	assets := trackers.DescriptionAssets{
		Description: "Body token",
		MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}},
		Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/normal.png"}},
	}
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
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
	result, err = New().prepareDescription(context.Background(), trackers.PreparationInput{Tracker: "ANT", Assets: &final})
	if err != nil {
		t.Fatalf("build final description: %v", err)
	}
	if result.Description != "Authoritative final token" {
		t.Fatalf("final description = %q", result.Description)
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
