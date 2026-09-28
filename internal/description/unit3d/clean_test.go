// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"
	"testing"
)

func TestCleanDescriptionPreservesComparisonAndImportsLaterScreenshots(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[align=left]Source &amp; Encode[/align]\r\n\r\n\r\n" +
		"[center][URL=https://www.imagebam.com/view/EXAMPLE][IMG]https://thumbs2.imagebam.com/aa/bb/cc/compare.jpg[/IMG][/URL][/center]\r\n[/spoiler]"
	screenshots := "[url=https://ibb.co/shot-one][img]https://i.ibb.co/example/shot-one.png[/img][/url]\n" +
		"[url=https://ibb.co/shot-two][img]https://i.ibb.co/example/shot-two.png[/img][/url]"
	description := "Release notes\n\n" + comparison + "\n\n" + screenshots

	report := CleanDescription(description, "https://aither.cc")
	if !strings.Contains(report.Description, comparison) {
		t.Fatalf("comparison BBCode was changed or removed: %q", report.Description)
	}
	if strings.Contains(report.Description, "shot-one.png") || strings.Contains(report.Description, "shot-two.png") {
		t.Fatalf("separate screenshot links remained in cleaned body: %q", report.Description)
	}
	if len(report.Images) != 2 || report.Images[0].RawURL != "https://i.ibb.co/example/shot-one.png" ||
		report.Images[1].RawURL != "https://i.ibb.co/example/shot-two.png" {
		t.Fatalf("expected two later screenshots, got %#v", report.Images)
	}
	if len(report.Notes) == 0 || !strings.Contains(report.Notes[0].Message, "comparison_blocks=1 image_tags=1") {
		t.Fatalf("expected comparison handling note, got %#v", report.Notes)
	}
	if body := CleanDescriptionBody(description, "https://aither.cc").Description; body != report.Description {
		t.Fatalf("body-only cleanup differs: %q", body)
	}
	if images := CleanDescriptionImages(description, "https://aither.cc").Images; len(images) != 2 || images[0].RawURL != report.Images[0].RawURL {
		t.Fatalf("image-only cleanup differs: %#v", images)
	}
}

func TestCleanDescriptionPreservesComparisonTag(t *testing.T) {
	comparison := `[comparison=Source, Encode][img]https://i.ibb.co/example/compare.png[/img][/comparison]`
	report := CleanDescription(comparison+"\n\n"+
		`[img]https://i.ibb.co/example/screenshot.png[/img]`, "https://aither.cc")
	if !strings.Contains(report.Description, comparison) || len(report.Images) != 1 ||
		report.Images[0].RawURL != "https://i.ibb.co/example/screenshot.png" {
		t.Fatalf("comparison should remain and separate screenshot should be selected: %#v", report)
	}
}

func TestCleanDescriptionPreservesNestedComparisonSpoiler(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[spoiler=Source][img]https://img.example/source.png[/img][/spoiler]\r\n" +
		"[img]https://img.example/encode.png[/img]\r\n[/spoiler]"
	report := CleanDescription(comparison+"\n\n[img]https://img.example/screen.png[/img]", "https://blu.example")
	if !strings.Contains(report.Description, comparison) || len(report.Images) != 1 ||
		report.Images[0].RawURL != "https://img.example/screen.png" {
		t.Fatalf("nested comparison was changed or imported as screenshots: %#v", report)
	}
	if body := StripScreenshotBlocks(comparison + "\n\n[center][img]https://img.example/screen.png[/img][/center]"); !strings.Contains(body, comparison) || strings.Contains(body, "screen.png") {
		t.Fatalf("builder strip changed nested comparison: %q", body)
	}
}

func TestCleanDescriptionPrefersScreenshotWrapperOverStandaloneCover(t *testing.T) {
	report := CleanDescriptionImages(
		"[img]https://covers.example/cover.jpg[/img]\n\n"+
			"[center][img]https://img.example/screenshot.png[/img][/center]",
		"https://aither.cc",
	)
	if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.example/screenshot.png" {
		t.Fatalf("expected wrapped screenshot to win over standalone cover, got %#v", report.Images)
	}
}

func TestCleanDescriptionImageNotesExplainSkippedCandidates(t *testing.T) {
	report := CleanDescriptionImages(
		"[center][img][/img]"+
			"[img]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img]"+
			"[img]https://thumbs2.imagebam.com/aa/bb/cc/compare.jpg[/img]"+
			"[url=https://img.example/screen.png][img]https://img.example/screen.png[/img][/url]"+
			"[url=https://img.example/screen.png][img]https://img.example/screen.png[/img][/url][/center]",
		"https://aither.cc",
	)
	if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.example/screen.png" {
		t.Fatalf("expected one usable image, got %#v", report.Images)
	}
	if len(report.Notes) != 1 {
		t.Fatalf("expected one image-selection note, got %#v", report.Notes)
	}
	for _, field := range []string{"candidates=5", "usable=1", "empty=1", "blocked=1", "unsupported_thumbnails=1", "duplicates=1"} {
		if !strings.Contains(report.Notes[0].Message, field) {
			t.Fatalf("missing %s from image note %q", field, report.Notes[0].Message)
		}
	}
	if strings.Contains(report.Notes[0].Message, "https://") {
		t.Fatalf("image URLs leaked into note: %q", report.Notes[0].Message)
	}
}

func TestCleanDescriptionPreservesSameHostImageURLs(t *testing.T) {
	report := CleanDescription(
		`[center][url=https://www.example.com/gallery][img]https://www.example.com/images/full.png[/img][/url][/center]`,
		"https://www.example.com",
	)

	if len(report.Images) != 1 {
		t.Fatalf("expected one image, got %d: %+v", len(report.Images), report.Images)
	}
	if report.Images[0].RawURL != "https://www.example.com/images/full.png" {
		t.Fatalf("expected raw URL preserved, got %q", report.Images[0].RawURL)
	}
	if report.Images[0].WebURL != "https://www.example.com/gallery" {
		t.Fatalf("expected web URL preserved, got %q", report.Images[0].WebURL)
	}
}

func TestCleanDescriptionUsesLinkedImageURLAsRawSource(t *testing.T) {
	report := CleanDescription(
		`[center][url=https://i.ibb.co/jkrgzQGv/04c944afef5a.png][img]https://wsrv.nl/?n=-1&ll&url=https%3A%2F%2Fi.ibb.co%2F8g76bf2D%2F04c944afef5a.png[/img][/url][/center]`,
		"https://example.com",
	)

	if len(report.Images) != 1 {
		t.Fatalf("expected one image, got %d: %+v", len(report.Images), report.Images)
	}
	if report.Images[0].ImgURL != "https://wsrv.nl/?n=-1&ll&url=https%3A%2F%2Fi.ibb.co%2F8g76bf2D%2F04c944afef5a.png" {
		t.Fatalf("expected thumbnail image URL preserved, got %q", report.Images[0].ImgURL)
	}
	if report.Images[0].RawURL != "https://i.ibb.co/jkrgzQGv/04c944afef5a.png" {
		t.Fatalf("expected linked full-size URL as raw URL, got %q", report.Images[0].RawURL)
	}
	if report.Images[0].WebURL != "https://i.ibb.co/jkrgzQGv/04c944afef5a.png" {
		t.Fatalf("expected web URL preserved, got %q", report.Images[0].WebURL)
	}
}

func TestCleanDescriptionConvertsPixhostCurrentDomainThumbURL(t *testing.T) {
	report := CleanDescription(
		`[center][url=https://pixhost.cc/show/11645/shot.png][img]https://t1.pixhost.cc/thumbs/11645/shot.png[/img][/url][/center]`,
		"https://example.com",
	)

	if len(report.Images) != 1 {
		t.Fatalf("expected one image, got %d: %+v", len(report.Images), report.Images)
	}
	if report.Images[0].RawURL != "https://img1.pixhost.cc/images/11645/shot.png" {
		t.Fatalf("expected pixhost raw URL conversion, got %q", report.Images[0].RawURL)
	}
}

func TestCleanDescriptionConvertsMixedCasePixhostThumbURL(t *testing.T) {
	report := CleanDescription(
		`[center][url=https://pixhost.cc/show/11645/shot.png][img]https://T1.PixHost.Cc/thumbs/11645/shot.png[/img][/url][/center]`,
		"https://example.com",
	)

	if len(report.Images) != 1 {
		t.Fatalf("expected one image, got %d: %+v", len(report.Images), report.Images)
	}
	if report.Images[0].RawURL != "https://img1.pixhost.cc/images/11645/shot.png" {
		t.Fatalf("expected pixhost raw URL conversion, got %q", report.Images[0].RawURL)
	}
}

func TestCleanDescriptionUsesOnlyImagePageOverBackupThumbnail(t *testing.T) {
	report := CleanDescription(
		"[url=https://onlyimage.org/image/Ab12][img]https://file.aither.cc/backup.png[/img][/url]",
		"https://aither.cc",
	)
	if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.onlyimage.org/Ab12.png" ||
		report.Images[0].WebURL != "https://onlyimage.org/image/Ab12" {
		t.Fatalf("OnlyImage original not selected: %#v", report.Images)
	}
}

func TestCleanDescriptionUsesLinkedWsrvSource(t *testing.T) {
	report := CleanDescription(
		"[url=https://wsrv.nl/?url=https%3A%2F%2Fimg.onlyimage.org%2FFull.md.png][img]https://file.aither.cc/backup.png[/img][/url]",
		"https://aither.cc",
	)
	if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.onlyimage.org/Full.png" {
		t.Fatalf("proxied full-size image not selected: %#v", report.Images)
	}
}

func TestCleanDescriptionUsesWsrvSourceHostForUnlinkedImage(t *testing.T) {
	report := CleanDescription(
		"[img]https://wsrv.nl/?url=https%3A%2F%2Fimg.onlyimage.org%2FFull.md.png[/img]",
		"https://aither.cc",
	)
	if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.onlyimage.org/Full.png" || report.Images[0].Host != "onlyimage" {
		t.Fatalf("proxied image host not updated: %#v", report.Images)
	}
}

func TestCleanDescriptionKeepsSupportedImageWhenLinkedFormatCannotBeRehosted(t *testing.T) {
	for _, extension := range []string{"avif", "bmp", "gif"} {
		t.Run(extension, func(t *testing.T) {
			report := CleanDescription(
				"[url=https://img.blutopia.cc/Full."+extension+"][img]https://img.blutopia.cc/Thumb.jpg[/img][/url]",
				"https://aither.cc",
			)
			if len(report.Images) != 1 || report.Images[0].RawURL != "https://img.blutopia.cc/Thumb.jpg" {
				t.Fatalf("unsupported linked image displaced JPEG: %#v", report.Images)
			}
		})
	}
}

func TestNormalizeRawImageURLRejectsPixhostSuffixHosts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "current suffix host",
			in:   "https://t1.evilpixhost.cc/thumbs/11645/shot.png",
			want: "https://t1.evilpixhost.cc/thumbs/11645/shot.png",
		},
		{
			name: "legacy suffix host",
			in:   "https://t1.evilpixhost.to/thumbs/11645/shot.png",
			want: "https://t1.evilpixhost.to/thumbs/11645/shot.png",
		},
		{
			name: "current exact host",
			in:   "https://pixhost.cc/thumbs/11645/shot.png",
			want: "https://pixhost.cc/images/11645/shot.png",
		},
		{
			name: "current subdomain",
			in:   "https://t1.pixhost.cc/thumbs/11645/shot.png",
			want: "https://img1.pixhost.cc/images/11645/shot.png",
		},
		{
			name: "mixed case current subdomain",
			in:   "https://T1.PixHost.Cc/thumbs/11645/shot.png",
			want: "https://img1.pixhost.cc/images/11645/shot.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeRawImageURL(tt.in)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizeLinkedRawImageURLPreservesPixhostSuffixShowURL(t *testing.T) {
	got, ok := normalizeLinkedRawImageURL("https://evilpixhost.cc/show/11645/shot.png")
	if !ok {
		t.Fatal("expected foreign suffix host to remain usable")
	}
	if got != "https://evilpixhost.cc/show/11645/shot.png" {
		t.Fatalf("expected foreign suffix host preserved, got %q", got)
	}
}

func TestReplaceSiteHostSkipsURLs(t *testing.T) {
	result := replaceSiteHost(
		"Visit www.example.com or https://www.example.com/path or HTTPS://www.example.com/full.png for details",
		"https://www.example.com",
	)

	const expected = "Visit example or https://www.example.com/path or HTTPS://www.example.com/full.png for details"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestReplaceSiteHostReplacesMixedCaseOutsideURLs(t *testing.T) {
	result := replaceSiteHost(
		"Visit WWW.Example.com or https://WWW.Example.com/path or HTTP://WWW.Example.com/full.png for details",
		"https://www.example.com",
	)

	const expected = "Visit example or https://WWW.Example.com/path or HTTP://WWW.Example.com/full.png for details"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestReplaceSiteHostNormalizesApexAndWWWAliasOutsideURLs(t *testing.T) {
	result := replaceSiteHost(
		"Visit AITHER.CC, WWW.Aither.cc, foo.aither.cc, www.aither.cc.uk, and https://www.aither.cc/path for details",
		"https://aither.cc",
	)

	const expected = "Visit aither, aither, foo.aither.cc, www.aither.cc.uk, and https://www.aither.cc/path for details"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}
