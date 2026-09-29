// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionLinksHostedThumbnail(t *testing.T) {
	t.Parallel()
	assets := trackers.DescriptionAssets{Screenshots: []api.ScreenshotImage{{
		ImgURL: "https://images.example.invalid/thumb.png",
		RawURL: "https://images.example.invalid/full.png",
		WebURL: "https://images.example.invalid/page",
	}}}
	want := "[url=https://images.example.invalid/page][img]https://images.example.invalid/thumb.png[/img][/url]"
	if got := buildDescription(assets); got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}
