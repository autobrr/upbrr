// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotBlockLinksHostedThumbnail(t *testing.T) {
	t.Parallel()
	images := []api.ScreenshotImage{{
		ImgURL: "https://images.example.invalid/thumb.png",
		RawURL: "https://images.example.invalid/full.png",
		WebURL: "https://images.example.invalid/page",
	}}
	want := "[center][url=https://images.example.invalid/page][img=350]https://images.example.invalid/thumb.png[/img][/url][/center]"
	if got := screenshotBlock(images); got != want {
		t.Fatalf("screenshot block = %q, want %q", got, want)
	}
}

func TestScreenshotBlockLinksFullImageWhenHostHasNoPage(t *testing.T) {
	t.Parallel()
	images := []api.ScreenshotImage{{
		ImgURL: "https://images.example.invalid/thumb.png",
		RawURL: "https://images.example.invalid/full.png",
	}}
	want := "[center][url=https://images.example.invalid/full.png][img=350]https://images.example.invalid/thumb.png[/img][/url][/center]"
	if got := screenshotBlock(images); got != want {
		t.Fatalf("screenshot block = %q, want %q", got, want)
	}
}
