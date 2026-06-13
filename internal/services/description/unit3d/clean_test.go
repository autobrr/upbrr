// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import "testing"

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
