// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

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
		MenuImages: []api.ScreenshotImage{{
			RawURL: "https://images.example.invalid/menu.png",
			ImgURL: "https://images.example.invalid/menu-thumb.png",
			WebURL: "https://images.example.invalid/menu-view",
		}},
		Screenshots: []api.ScreenshotImage{{
			RawURL: "https://images.example.invalid/normal.png",
			ImgURL: "https://images.example.invalid/normal-thumb.png",
			WebURL: "https://images.example.invalid/normal-view",
		}},
	}
	result, err := (definition{}).BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "HDS",
		AppConfig: config.Config{Description: config.DescriptionSettingsConfig{
			DiscMenuHeader:   "Disc menu token",
			ScreenshotHeader: "Screenshots token",
		}},
		Assets: &assets,
	})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	assertDescriptionTokensInOrder(t, result.Description, "Body token", "Disc menu token", "menu-thumb.png", "Screenshots token", "normal-thumb.png")

	final := trackers.DescriptionAssets{Description: " Authoritative final token ", Final: true, MenuImages: assets.MenuImages}
	result, err = (definition{}).BuildDescription(context.Background(), trackers.DescriptionRequest{Tracker: "HDS", Assets: &final})
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
