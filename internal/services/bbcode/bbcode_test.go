// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import (
	"strings"
	"testing"
)

func TestCleanUnit3DDescription(t *testing.T) {
	desc := "[url=https://blutopia.xyz/torrents/123]Title[/url]\n[img]https://example.com/a.jpg[/img]\n[img]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img]"
	report := CleanUnit3DDescription(desc, "https://blutopia.xyz")

	if report.Description != "" {
		t.Fatalf("expected description to be discarded, got %q", report.Description)
	}
	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://example.com/a.jpg" {
		t.Fatalf("unexpected image url %q", report.Images[0].ImgURL)
	}
}

func TestCleanUnit3DDescriptionConvertsPixhostThumbRawURL(t *testing.T) {
	desc := "[url=https://pixhost.to/show/11645/685417513_file-upload-0.png][img]https://t1.pixhost.to/thumbs/11645/685417513_file-upload-0.png[/img][/url]"
	report := CleanUnit3DDescription(desc, "https://example.org")

	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].RawURL != "https://img1.pixhost.to/images/11645/685417513_file-upload-0.png" {
		t.Fatalf("unexpected converted raw url %q", report.Images[0].RawURL)
	}
}

func TestCleanUnit3DDescriptionRemovesOrphanedTonemapAndEmptyAlign(t *testing.T) {
	desc := "[center][code] Screenshots have been tonemapped for reference [/code][/center]\n\n[align=center]\n[url=https://img.example/a.jpg][img]https://img.example/a.jpg[/img][/url]\n[/align]"
	report := CleanUnit3DDescription(desc, "https://example.org")

	if report.Description != "" {
		t.Fatalf("expected description to be discarded, got %q", report.Description)
	}
	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://img.example/a.jpg" {
		t.Fatalf("unexpected image url %q", report.Images[0].ImgURL)
	}
}

func TestCleanUnit3DDescriptionKeepsFirstScreenshotSetAfterTMDBPoster(t *testing.T) {
	desc := "[center][img=300]https://image.tmdb.org/t/p/original/poster.png[/img][/center]\n\n" +
		"[center]Stranger.Things.S05E01[/center]\n\n" +
		"[center]" +
		"[url=https://imgbox.com/A][img=300]https://images2.imgbox.com/aa/aa/A_o.png[/img][/url] " +
		"[url=https://imgbox.com/B][img=300]https://images2.imgbox.com/bb/bb/B_o.png[/img][/url] " +
		"[url=https://imgbox.com/C][img=300]https://images2.imgbox.com/cc/cc/C_o.png[/img][/url]" +
		"[/center]\n\n" +
		"[center][spoiler=Other files]" +
		"[url=https://imgbox.com/D][img=300]https://images2.imgbox.com/dd/dd/D_o.png[/img][/url]" +
		"[/spoiler][/center]"

	report := CleanUnit3DDescription(desc, "https://example.org")

	if report.Description != "" {
		t.Fatalf("expected description to be discarded, got %q", report.Description)
	}
	if len(report.Images) != 3 {
		t.Fatalf("expected first screenshot set only (3 images), got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://images2.imgbox.com/aa/aa/A_o.png" {
		t.Fatalf("unexpected first image %q", report.Images[0].ImgURL)
	}
	if report.Images[1].ImgURL != "https://images2.imgbox.com/bb/bb/B_o.png" {
		t.Fatalf("unexpected second image %q", report.Images[1].ImgURL)
	}
	if report.Images[2].ImgURL != "https://images2.imgbox.com/cc/cc/C_o.png" {
		t.Fatalf("unexpected third image %q", report.Images[2].ImgURL)
	}
}

func TestCleanHDBDescription(t *testing.T) {
	desc := "Text\n[url=https://imgbox.com/abc][img]https://thumbs2.imgbox.com/abc_t.png[/img][/url]\nhttps://hdbits.org/x"
	report := CleanHDBDescription(desc)

	if report.Description != "Text" {
		t.Fatalf("expected description to be cleaned, got %q", report.Description)
	}
	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].RawURL != "https://images2.imgbox.com/abc_o.png" {
		t.Fatalf("expected raw url conversion, got %q", report.Images[0].RawURL)
	}
}

func TestCleanHDBDescriptionRemovesEmptyURLTags(t *testing.T) {
	desc := "Text\n[url=][/url] [url=][/url] [url=][/url] [url=][/url]"
	report := CleanHDBDescription(desc)
	if report.Description != "Text" {
		t.Fatalf("expected empty url tags removed, got %q", report.Description)
	}
}

func TestCleanBHDDescription(t *testing.T) {
	desc := "hello\n[url=https://example.com/full/a][img]https://example.com/a.png[/img][/url]\n[img]https://example.com/b.webp[/img]"
	report := CleanBHDDescription(desc, BHDOptions{Framestor: true, Flux: true})

	if report.Description != "[code]hello[/code]" {
		t.Fatalf("expected code block description, got %q", report.Description)
	}
	if len(report.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://example.com/a.png" || report.Images[0].WebURL != "https://example.com/full/a" {
		t.Fatalf("unexpected first image %+v", report.Images[0])
	}
	if report.Images[1].ImgURL != "https://example.com/b.webp" {
		t.Fatalf("unexpected second image %+v", report.Images[1])
	}
	if len(report.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(report.Artifacts))
	}
	if report.Artifacts[0].Name != "bhd.nfo" {
		t.Fatalf("unexpected artifact name %q", report.Artifacts[0].Name)
	}
}

func TestCleanBHDDescriptionRemovesOrphanedEmptyAlign(t *testing.T) {
	desc := "[center][code] Screenshots have been tonemapped for reference [/code][/center]\n\n[align=center]\n[url=https://img.example/a.jpg][img]https://img.example/a.jpg[/img][/url]\n[/align]"
	report := CleanBHDDescription(desc, BHDOptions{})

	if report.Description != "" {
		t.Fatalf("expected empty wrapper bbcode removed, got %q", report.Description)
	}
	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://img.example/a.jpg" {
		t.Fatalf("unexpected image url %q", report.Images[0].ImgURL)
	}
}

func TestCleanPTPDescription(t *testing.T) {
	desc := "&bull; item\n[quote]x[/quote]\nhttps://example.com/test.jpg"
	report := CleanPTPDescription(desc, "")

	if report.Description != "- item\n[code]x[/code]" {
		t.Fatalf("unexpected description %q", report.Description)
	}
	if len(report.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(report.Images))
	}
	if report.Images[0].ImgURL != "https://example.com/test.jpg" {
		t.Fatalf("unexpected image url %q", report.Images[0].ImgURL)
	}
}

func TestCleanPTPDescriptionRemovesWidthStyledImageBlocks(t *testing.T) {
	desc := `[align=center]
[url=https://pixhost.to/fv71hr.png][img width=350]https://pixhost.to/fv71hr.png[/img][/url]
[/align]`
	report := CleanPTPDescription(desc, "")

	if strings.Contains(report.Description, "[url=][img][/img][/url]") {
		t.Fatalf("expected no orphaned empty image tags, got %q", report.Description)
	}
	if strings.TrimSpace(report.Description) != "" {
		t.Fatalf("expected screenshot-only description to be removed, got %q", report.Description)
	}
}

func TestNormalizeImageRawURLConvertsPixhostThumbURL(t *testing.T) {
	value := normalizeImageRawURL("https://t1.pixhost.to/thumbs/11645/685417513_file-upload-0.png")
	if value != "https://img1.pixhost.to/images/11645/685417513_file-upload-0.png" {
		t.Fatalf("unexpected normalized value %q", value)
	}
}

func TestIsOnlyBBCode(t *testing.T) {
	if !isOnlyBBCode("[b][/b]") {
		t.Fatal("expected only bbcode to be true")
	}
	if isOnlyBBCode("text [b][/b]") {
		t.Fatal("expected text to be false")
	}
}
