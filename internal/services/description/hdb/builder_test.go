// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionAddsWebDLSource(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:            "WEBDL",
		ServiceLongName: "Netflix",
	}
	description, err := BuildDescription(context.Background(), meta, config.Config{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(description, "This release is sourced from Netflix") {
		t.Fatalf("expected web source quote, got %q", description)
	}
}

func TestBuildDescriptionTransformsBaseAndAddsScreens(t *testing.T) {
	meta := api.PreparedMetadata{
		Options: api.UploadOptions{Screens: 1},
	}
	base := "[code]x[/code]\n[spoiler=Comp][comparison=A, B]https://img/a.jpg https://img/b.jpg[/comparison][/spoiler]\n[size=3][img=300]https://img/x.jpg[/img][/size]"
	screens := []api.ScreenshotImage{{ImgURL: "https://img/s1.jpg", WebURL: "https://web/s1"}, {ImgURL: "https://img/s2.jpg"}}

	description, err := BuildDescription(context.Background(), meta, config.Config{}, base, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(description, "[font=monospace]x[/font]") {
		t.Fatalf("expected code tag conversion, got %q", description)
	}
	if !strings.Contains(description, "[hide=Comp]") {
		t.Fatalf("expected spoiler-to-hide conversion, got %q", description)
	}
	if !strings.Contains(description, "[center][url=https://web/s1][img]https://img/s1.jpg[/img][/url][/center]") {
		t.Fatalf("expected screenshot section with one image due to limit, got %q", description)
	}
	if strings.Contains(description, "https://img/s2.jpg") {
		t.Fatalf("expected second screenshot omitted by limit, got %q", description)
	}
}

func TestBuildDescriptionReplacesExistingScreenshotBlock(t *testing.T) {
	meta := api.PreparedMetadata{
		Options: api.UploadOptions{Screens: 4},
	}
	base := `[align=center]
[url=https://pixhost.to/8ca234.png][img width=350]https://pixhost.to/8ca234.png[/img][/url]
[url=https://pixhost.to/4oh0bz.png][img width=350]https://pixhost.to/4oh0bz.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`
	screens := []api.ScreenshotImage{
		{ImgURL: "https://t.hdbits.org/51q8jo2.jpg", RawURL: "https://img.hdbits.org/51q8jo2.jpg", WebURL: "https://img.hdbits.org/51q8jo2"},
		{ImgURL: "https://t.hdbits.org/w0S7ltI.jpg", RawURL: "https://img.hdbits.org/w0S7ltI.jpg", WebURL: "https://img.hdbits.org/w0S7ltI"},
	}

	description, err := BuildDescription(context.Background(), meta, config.Config{}, base, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(description, "pixhost.to/8ca234.png") || strings.Contains(description, "pixhost.to/4oh0bz.png") {
		t.Fatalf("expected old screenshot block to be removed, got %q", description)
	}
	if count := strings.Count(description, "[center][url=https://img.hdbits.org/"); count != 1 {
		t.Fatalf("expected one rebuilt screenshot section, got %d in %q", count, description)
	}
	if !strings.Contains(description, "img.hdbits.org/51q8jo2") || !strings.Contains(description, "img.hdbits.org/w0S7ltI") {
		t.Fatalf("expected new HDB screenshots, got %q", description)
	}
}

func TestBuildDescriptionPreservesExistingScreensWhenNoNewScreensProvided(t *testing.T) {
	meta := api.PreparedMetadata{
		Options: api.UploadOptions{Screens: 4},
	}
	base := `[align=center]
[url=https://pixhost.to/8ca234.png][img width=350]https://pixhost.to/8ca234.png[/img][/url]
[url=https://pixhost.to/4oh0bz.png][img width=350]https://pixhost.to/4oh0bz.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`

	description, err := BuildDescription(context.Background(), meta, config.Config{}, base, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(description, "pixhost.to/8ca234.png") || !strings.Contains(description, "pixhost.to/4oh0bz.png") {
		t.Fatalf("expected existing screenshot block preserved, got %q", description)
	}
	if strings.Contains(description, "upbrr") {
		t.Fatalf("expected default signature removed, got %q", description)
	}
}
