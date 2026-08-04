// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSwapImageURLs(t *testing.T) {
	images := []api.ScreenshotImage{
		{
			ImgURL: "https://host.invalid/medium.png",
			RawURL: "https://host.invalid/full.png",
			WebURL: "https://host.invalid/page",
		},
		{
			ImgURL: "",
			RawURL: "https://host.invalid/full2.png",
			WebURL: "https://host.invalid/page2",
		},
	}
	got := swapImageURLs(images)

	// First image: full moves to WebURL (link), medium moves to RawURL (display).
	if got[0].WebURL != "https://host.invalid/full.png" || got[0].RawURL != "https://host.invalid/medium.png" {
		t.Fatalf("expected swapped URLs, got web=%q raw=%q", got[0].WebURL, got[0].RawURL)
	}
	// Second image lacks a medium thumbnail and is left unchanged.
	if got[1].WebURL != "https://host.invalid/page2" || got[1].RawURL != "https://host.invalid/full2.png" {
		t.Fatalf("expected unchanged URLs, got web=%q raw=%q", got[1].WebURL, got[1].RawURL)
	}
	// Input slice must not be mutated.
	if images[0].WebURL != "https://host.invalid/page" {
		t.Fatalf("input slice mutated: %q", images[0].WebURL)
	}
}
