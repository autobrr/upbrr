// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"fmt"
	"testing"
)

func TestImportedScreenshotEvidenceCountsDistinctInspectedImages(t *testing.T) {
	for _, tc := range []struct {
		name                string
		formats             []string
		inspected, lossless int
	}{
		{"eight qualifying", []string{"png", "png", "png", "jpeg", "jpeg", "jpeg", "jpeg", "jpeg"}, 8, 3},
		{"eight unknown", []string{"", "", "", "", "", "", "", ""}, 0, 0},
		{"mixed", []string{"png", "jpeg", "", "", "", "", "", ""}, 2, 1},
		{"confirmed insufficient", []string{"png", "jpeg", "jpeg", "jpeg", "jpeg", "jpeg", "jpeg", "jpeg"}, 8, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exact := &ExactMediaAssets{}
			for i, format := range tc.formats {
				path := fmt.Sprintf("imported/%d.jpeg", i)
				exact.Screenshots = append(exact.Screenshots, ScreenshotImage{
					Path:    path,
					Purpose: ScreenshotPurposeFinal,
					Format:  format,
				})
				exact.ScreenshotUploads = append(exact.ScreenshotUploads, UploadedImageLink{ImagePath: path, RawURL: fmt.Sprintf("https://images.example/%d", i)})
			}
			exact.Screenshots = append(exact.Screenshots, exact.Screenshots[0], ScreenshotImage{Purpose: ScreenshotPurposeFinal, Format: "png"})
			exact.ScreenshotUploads = append(exact.ScreenshotUploads, exact.ScreenshotUploads[0])
			got := deriveValidationAssetFacts(UploadSubject{ExactMedia: exact})
			if got.Screenshots.Count != 8 || got.HostedScreenshots.Count != 8 || got.Screenshots.InspectedFormatCount != tc.inspected || got.Screenshots.LosslessCount != tc.lossless {
				t.Fatalf("inaccurate distinct evidence: %+v / %+v", got.Screenshots, got.HostedScreenshots)
			}
		})
	}
}

func TestConflictingImportedScreenshotEvidenceIsUnknown(t *testing.T) {
	exact := &ExactMediaAssets{Screenshots: []ScreenshotImage{
		{
			Path:    "same",
			Purpose: ScreenshotPurposeFinal,
			Format:  "png",
		},
		{
			Path:    "same",
			Purpose: ScreenshotPurposeFinal,
			Format:  "jpeg",
		},
	}}
	got := deriveValidationAssetFacts(UploadSubject{ExactMedia: exact}).Screenshots
	if got.Count != 1 || got.InspectedFormatCount != 0 || got.LosslessCount != 0 {
		t.Fatalf("contradictory image manufactured evidence: %+v", got)
	}
}

func TestImportedScreenshotReferencesDetachExactIdentity(t *testing.T) {
	exact := &ExactMediaAssets{
		Screenshots: []ScreenshotImage{{
			Path:    "private/image",
			Purpose: ScreenshotPurposeFinal,
			Format:  "png",
		}},
		ScreenshotUploads: []UploadedImageLink{{
			ImagePath: "private/image",
			RawURL:    "https://images.example/full",
			ImgURL:    "https://images.example/thumb",
			WebURL:    "https://images.example/page",
		}, {ImagePath: "unselected", RawURL: "https://images.example/other"}},
	}
	facts := NewTrackerValidationSubject(UploadSubject{ExactMedia: exact}, "LST").AssetFacts
	if len(facts.ScreenshotReferences) != 1 || facts.ScreenshotReferences[0].ID == "" || string(facts.ScreenshotReferences[0].ID) == "private/image" {
		t.Fatalf("invalid prepared references: %+v", facts.ScreenshotReferences)
	}
	exact.ScreenshotUploads[0].RawURL = "changed"
	if facts.ScreenshotReferences[0].RawURL != "https://images.example/full" {
		t.Fatal("reference aliases mutable exact evidence")
	}
}

func TestScreenshotReferencesBindTrackerAndSurviveMaterialization(t *testing.T) {
	exact := &ExactMediaAssets{
		Screenshots: []ScreenshotImage{{Path: "before", Purpose: ScreenshotPurposeFinal}},
		ScreenshotUploads: []UploadedImageLink{{
			ImagePath:  "before",
			RawURL:     "https://images.example/a",
			UsageScope: "tracker:LST",
		}, {
			ImagePath:  "before",
			RawURL:     "https://images.example/b",
			UsageScope: "tracker:OTHER",
		}},
	}
	before := NewTrackerValidationSubject(UploadSubject{ExactMedia: exact}, "LST").AssetFacts.ScreenshotReferences
	if len(before) != 1 || before[0].RawURL != "https://images.example/a" {
		t.Fatalf("foreign host reference escaped scope: %+v", before)
	}
	exact.Screenshots[0].Path = "after"
	for i := range exact.ScreenshotUploads {
		exact.ScreenshotUploads[i].ImagePath = "after"
	}
	after := NewTrackerValidationSubject(UploadSubject{ExactMedia: exact}, "LST").AssetFacts.ScreenshotReferences
	if len(after) != 1 || before[0] != after[0] {
		t.Fatal("materialization changed selected-image reference")
	}
}

func TestHostedScreenshotCountUsesDistinctTrackerReferences(t *testing.T) {
	exact := &ExactMediaAssets{}
	for i := range 3 {
		path := fmt.Sprintf("selected/%d", i)
		exact.Screenshots = append(exact.Screenshots, ScreenshotImage{
			Path:    path,
			Purpose: ScreenshotPurposeFinal,
			Format:  "png",
		})
		exact.ScreenshotUploads = append(exact.ScreenshotUploads, UploadedImageLink{
			ImagePath:  path,
			RawURL:     "https://images.example/same",
			UsageScope: "tracker:LST",
		}, UploadedImageLink{
			ImagePath:  path,
			RawURL:     fmt.Sprintf("https://images.example/other/%d", i),
			UsageScope: "tracker:OTHER",
		})
	}
	got := NewTrackerValidationSubject(UploadSubject{ExactMedia: exact}, "LST").AssetFacts.HostedScreenshots
	if got.Count != 1 {
		t.Fatalf("host aliases or foreign tracker links inflated count: %+v", got)
	}
}

func TestScreenshotAliasesRemainLinkedThroughUnrenderedVariants(t *testing.T) {
	refs := []ScreenshotReference{
		{ID: "a", RawURL: "https://images.example/a"},
		{ID: "a", RawURL: "https://images.example/shared"},
		{ID: "b", RawURL: "https://images.example/shared"},
		{ID: "b", RawURL: "https://images.example/b"},
	}
	if got := CountDistinctScreenshotReferences(refs, []ScreenshotReference{refs[0], refs[3]}); got != 1 {
		t.Fatalf("unrendered alias split one image: %d", got)
	}
}
