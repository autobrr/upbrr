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
