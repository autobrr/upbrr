// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionUsesMediaInfoAndNotes(t *testing.T) {
	dir := t.TempDir()
	miPath := filepath.Join(dir, "MEDIAINFO.txt")
	if err := os.WriteFile(miPath, []byte("MI_CONTENT"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	meta := api.PreparedMetadata{
		MediaInfoTextPath: miPath,
	}

	desc, err := BuildDescription(context.Background(), meta, config.Config{}, "[quote]note[/quote]", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(desc, "[mediainfo]MI_CONTENT[/mediainfo]") {
		t.Fatalf("expected mediainfo block, got %q", desc)
	}
	if !strings.Contains(desc, "[spoiler=Notes]note[/spoiler]") {
		t.Fatalf("expected notes spoiler, got %q", desc)
	}
}

func TestBuildDescriptionIncludesTonemapAndScreenshots(t *testing.T) {
	meta := api.PreparedMetadata{HDR: "HDR10"}
	cfg := config.Config{
		ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true},
		Description:        config.DescriptionSettingsConfig{TonemappedHeader: "[center]tone[/center]"},
	}
	screens := []api.ScreenshotImage{{RawURL: "https://raw/1", ImgURL: "https://img/1.jpg"}}

	desc, err := BuildDescription(context.Background(), meta, cfg, "", screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(desc, "[center]tone[/center]") {
		t.Fatalf("expected tonemap header, got %q", desc)
	}
	if !strings.Contains(desc, "[url=https://raw/1][img=250]https://img/1.jpg[/img][/url]") {
		t.Fatalf("expected mtv screenshot bbcode, got %q", desc)
	}
}

func TestBuildDescriptionSkipsTonemapHeaderForNonHDR(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{
		ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true},
		Description:        config.DescriptionSettingsConfig{TonemappedHeader: "[center]tone[/center]"},
	}
	screens := []api.ScreenshotImage{{RawURL: "https://raw/1", ImgURL: "https://img/1.jpg"}}

	desc, err := BuildDescription(context.Background(), meta, cfg, "", screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(desc, "[center]tone[/center]") {
		t.Fatalf("did not expect tonemap header for non-HDR content, got %q", desc)
	}
}

func TestBuildDescriptionStripsNFOFromNotes(t *testing.T) {
	meta := api.PreparedMetadata{}
	kept := "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n\nActual note"

	desc, err := BuildDescription(context.Background(), meta, config.Config{}, kept, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(desc, "scene nfo") {
		t.Fatalf("expected nfo removed from notes, got %q", desc)
	}
	if !strings.Contains(desc, "[spoiler=Notes]Actual note[/spoiler]") {
		t.Fatalf("expected cleaned notes spoiler, got %q", desc)
	}
}

func TestBuildDescriptionStripsImagesFromNotesWhenScreenshotsProvided(t *testing.T) {
	meta := api.PreparedMetadata{}
	kept := "[url=https://raw/1][img=250]https://img/1.jpg[/img][/url]\n\nSome note"
	screens := []api.ScreenshotImage{{RawURL: "https://raw/1", ImgURL: "https://img/1.jpg"}}

	desc, err := BuildDescription(context.Background(), meta, config.Config{}, kept, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(desc, "https://img/1.jpg") != 1 {
		t.Fatalf("expected screenshot included once, got %q", desc)
	}
	if !strings.Contains(desc, "[spoiler=Notes]Some note[/spoiler]") {
		t.Fatalf("expected notes without duplicated screenshot tags, got %q", desc)
	}
}

func TestBuildDescriptionStripsWidthImagesAndSignatureFromNotes(t *testing.T) {
	meta := api.PreparedMetadata{}
	kept := `[align=center]
[url=https://pixhost.to/fv71hr.png][img width=350]https://pixhost.to/fv71hr.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`
	screens := []api.ScreenshotImage{{RawURL: "https://pixhost.to/fv71hr.png", ImgURL: "https://pixhost.to/fv71hr.png"}}

	desc, err := BuildDescription(context.Background(), meta, config.Config{}, kept, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(desc, "[spoiler=Notes]") {
		t.Fatalf("expected notes section omitted when only screenshots/signature remain, got %q", desc)
	}
	if strings.Count(desc, "https://pixhost.to/fv71hr.png") != 2 {
		t.Fatalf("expected one screenshot block only, got %q", desc)
	}
	if strings.Contains(desc, "upbrr") {
		t.Fatalf("expected default signature removed from notes, got %q", desc)
	}
}
