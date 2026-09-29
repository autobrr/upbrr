// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionLinksHostedThumbnailAndKeepsRawFallback(t *testing.T) {
	t.Parallel()
	assets := trackers.DescriptionAssets{Screenshots: []api.ScreenshotImage{
		{ImgURL: "https://images.example.invalid/thumb.png", RawURL: "https://images.example.invalid/full.png"},
		{RawURL: "https://images.example.invalid/other.png"},
	}}
	got := buildDescription(api.UploadSubject{}, assets)
	for _, want := range []string{
		"[url=https://images.example.invalid/full.png][img]https://images.example.invalid/thumb.png[/img][/url]",
		"[img]https://images.example.invalid/other.png[/img]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("description missing %q: %q", want, got)
		}
	}
}
