// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDComparisonMarkupStaysExactAndSeparateImagesImport(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[align=left]Source &amp; Encode[/align]\r\n\r\n\r\n" +
		"[center][img]https://img.example/compare.png[/img][/center]\r\n[/spoiler]"
	screenshot := "[url=https://img.example/screen-page][img]https://img.example/screen.png[/img][/url]"
	source := "Notes\n\n" + comparison + "\n\n" + screenshot
	report := CleanDescription(source, BBCodeOptions{})
	if !strings.Contains(report.Description, comparison) || len(report.Images) != 1 ||
		report.Images[0].RawURL != "https://img.example/screen.png" {
		t.Fatalf("comparison preservation or screenshot extraction failed: %#v", report)
	}
	got := buildDescription(api.UploadSubject{}, config.Config{}, trackers.DescriptionAssets{Description: source})
	if !strings.Contains(got, comparison) || strings.Count(got, "https://img.example/screen.png") != 1 {
		t.Fatalf("BHD builder changed comparison or duplicated screenshot: %q", got)
	}
}

func TestBHDNestedComparisonMarkupStaysExact(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[spoiler=Source][img]https://img.example/source.png[/img][/spoiler]\r\n" +
		"[img]https://img.example/encode.png[/img]\r\n[/spoiler]"
	source := comparison + "\n\n[img]https://img.example/screen.png[/img]"
	report := CleanDescription(source, BBCodeOptions{})
	if !strings.Contains(report.Description, comparison) || len(report.Images) != 1 ||
		report.Images[0].RawURL != "https://img.example/screen.png" {
		t.Fatalf("nested BHD comparison changed or imported: %#v", report)
	}
	if rendered := buildDescription(api.UploadSubject{}, config.Config{}, trackers.DescriptionAssets{Description: source}); !strings.Contains(rendered, comparison) {
		t.Fatalf("BHD builder changed nested comparison: %q", rendered)
	}
}

func TestBHDNormalImageSharedWithComparisonStaysImportable(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	comparison := "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]"
	report := CleanDescription(comparison+"\n[img]"+imageURL+"[/img]", BBCodeOptions{})
	if !strings.Contains(report.Description, comparison) || strings.Count(report.Description, imageURL) != 1 ||
		len(report.Images) != 1 || report.Images[0].RawURL != imageURL {
		t.Fatalf("BHD comparison changed or shared normal image was lost: %#v", report)
	}
}

func TestBHDFluxComparisonRendersOutsideCode(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[img]https://img.example/compare.png[/img]\r\n[/spoiler]"
	source := "Before\n\n" + comparison + "\n\nAfter"
	report := CleanDescription(source, BBCodeOptions{Flux: true})
	if report.Description != "[code]Before[/code]\n\n"+comparison+"\n\n[code]After[/code]" {
		t.Fatalf("Flux cleaner wrapped comparison as code: %q", report.Description)
	}
	got := buildDescription(api.UploadSubject{Tag: "FLUX"}, config.Config{}, trackers.DescriptionAssets{Description: source})
	if !strings.Contains(got, report.Description) {
		t.Fatalf("BHD builder changed Flux comparison markup: %q", got)
	}
}
