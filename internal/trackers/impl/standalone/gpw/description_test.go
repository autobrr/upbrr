// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotBlockLinksHostedThumbnailAndKeepsRawFallback(t *testing.T) {
	t.Parallel()
	images := []api.ScreenshotImage{
		{ImgURL: "https://images.example.invalid/thumb.png", RawURL: "https://images.example.invalid/full.png"},
		{RawURL: "https://images.example.invalid/other.png"},
	}
	want := "[center][url=https://images.example.invalid/full.png][img]https://images.example.invalid/thumb.png[/img][/url] " +
		"[img]https://images.example.invalid/other.png[/img][/center]"
	if got := screenshotBlock(images); got != want {
		t.Fatalf("screenshot block = %q, want %q", got, want)
	}
}
