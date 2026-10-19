// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"testing"
)

func TestCollectionLabelsAndURLBoundary(t *testing.T) {
	t.Parallel()
	if got := CollectionLabel(`C:\private\A_[title].png`, "Fallback"); got != "A title.png" {
		t.Fatalf("label = %q", got)
	}
	if got := CollectionLabel(strings.Repeat("a", 200), "Fallback"); len(got) != 120 {
		t.Fatalf("label length = %d", len(got))
	}
	record := ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/abc-123",
		Purpose:            ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []ImageCollectionMember{{Identity: "content", Label: "Image"}},
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://slow.pics/c/a", "https://slow.pics.evil.invalid/c/a", "https://slow.pics/c/a?token=x", "https://slow.pics/c/a#x", "https://slow.pics/c/a/b", "https://slow.pics/c/a%5Db", "https://user@slow.pics/c/a"} {
		bad := record
		bad.URL = raw
		if bad.Validate() == nil {
			t.Errorf("unsafe collection URL accepted: %s", raw)
		}
	}
	for _, purpose := range []ScreenshotPurpose{ScreenshotPurposeFinal, ScreenshotPurposeMenu, ScreenshotPurposePreview, ""} {
		bad := record
		bad.Purpose = purpose
		if bad.Validate() == nil {
			t.Errorf("ordinary purpose accepted: %s", purpose)
		}
	}
	record.Completed = false
	if record.Validate() == nil {
		t.Fatal("partial collection accepted")
	}
}

func TestCombinedAnalysisCollectionCategory(t *testing.T) {
	t.Parallel()
	for _, purpose := range []ScreenshotPurpose{ScreenshotPurposeAudioAnalysis, ScreenshotPurposeHDRAnalysis, ScreenshotPurposeComparison, ScreenshotPurposeFinal, ScreenshotPurposeMenu, ScreenshotPurposePreview} {
		if got := ValidImageCollectionCategory(purpose, ImageCollectionCategoryAnalysis); got != purpose.IsAnalysis() {
			t.Errorf("purpose=%s allowed=%t", purpose, got)
		}
	}
}
