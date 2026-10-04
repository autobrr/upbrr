// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSynthesizeScreenshotSlotsSelectionsSkipDecorativeImages(t *testing.T) {
	t.Parallel()
	const (
		logoURL   = "https://images.example.invalid/logo.png"
		posterURL = "https://image.tmdb.org/t/p/original/poster.jpg"
		firstURL  = "https://images.example.invalid/first.png"
		secondURL = "https://images.example.invalid/second.png"
		logo      = "[img]" + logoURL + "[/img]"
		poster    = "[center][img]" + posterURL + "[/img][/center]"
		first     = "[center][img]" + firstURL + "[/img][/center]"
		second    = "[center][img]" + secondURL + "[/img][/center]"
	)
	for _, tc := range []struct {
		name           string
		description    string
		urls           []string
		selectedOrders []int
	}{
		{
			name:           "screenshots only",
			description:    first + second,
			urls:           []string{firstURL, secondURL},
			selectedOrders: []int{0, 1},
		},
		{
			name:           "leading logo and poster",
			description:    logo + poster + first + second,
			urls:           []string{logoURL, posterURL, firstURL, secondURL},
			selectedOrders: []int{0, 1},
		},
		{
			name:           "interleaved decorative images",
			description:    first + logo + poster + second,
			urls:           []string{firstURL, logoURL, posterURL, secondURL},
			selectedOrders: []int{0, 1},
		},
		{
			name:           "unselected screenshot stays excluded",
			description:    logo + first + second,
			urls:           []string{logoURL, firstURL, secondURL},
			selectedOrders: []int{0},
		},
		{
			name:           "unselected first screenshot preserves later order",
			description:    logo + poster + first + second,
			urls:           []string{logoURL, posterURL, firstURL, secondURL},
			selectedOrders: []int{1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := t.TempDir()
			available := []api.ScreenshotFinalSelection{
				{
					ImagePath: filepath.Join(sourcePath, "first.png"),
					DiscID:    "first-disc",
					Order:     0,
				},
				{
					ImagePath: filepath.Join(sourcePath, "second.png"),
					DiscID:    "second-disc",
					Order:     1,
				},
			}
			selections := make([]api.ScreenshotFinalSelection, 0, len(tc.selectedOrders))
			for _, order := range tc.selectedOrders {
				selections = append(selections, available[order])
			}
			repo := &stubRepo{selections: selections}
			selectedByURL := make(map[string]api.ScreenshotFinalSelection, len(selections))
			for _, selection := range selections {
				selectedByURL[[]string{firstURL, secondURL}[selection.Order]] = selection
				repo.uploads = append(repo.uploads, api.UploadedImageLink{
					ImagePath:  selection.ImagePath,
					Host:       "imgbb",
					UsageScope: "global",
					RawURL:     "https://rehost.example.invalid/" + filepath.Base(selection.ImagePath),
				})
			}
			slots, err := synthesizeScreenshotSlots(t.Context(), "AITHER", api.UploadSubject{
				SourcePath:          sourcePath,
				DescriptionOverride: tc.description,
			}, repo, api.NopLogger{}, nil, descriptionAssetsTestRegistry(t))
			if err != nil {
				t.Fatalf("synthesize screenshot slots: %v", err)
			}
			if len(slots) != len(tc.urls) {
				t.Fatalf("slot count = %d, want %d: %#v", len(slots), len(tc.urls), slots)
			}
			for index, slot := range slots {
				selection, selected := selectedByURL[tc.urls[index]]
				if slot.OriginalURL != tc.urls[index] || slot.OriginalKey != tc.urls[index] ||
					slot.ImagePath != selection.ImagePath || slot.DiscID != selection.DiscID || slot.RenderInScreenshots != selected {
					t.Errorf("slot %d shifted selection or changed image identity: %#v", index, slot)
				}
				if selected {
					if len(slot.Variants) != 1 || slot.Variants[0].ImagePath != selection.ImagePath {
						t.Errorf("slot %d received wrong rehosted image: %#v", index, slot.Variants)
					}
				} else if len(slot.Variants) != 0 {
					t.Errorf("unselected slot %d received screenshot variants: %#v", index, slot.Variants)
				}
			}
		})
	}
}

func TestAttachSelectionPathsToSlotsPreservesExistingPathsAndPositions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	decorative := api.ScreenshotSlot{
		ImagePath:   filepath.Join(dir, "logo.png"),
		DiscID:      "logo-disc",
		OriginalKey: "logo",
	}
	existing := api.ScreenshotSlot{
		ImagePath:           filepath.Join(dir, "existing.png"),
		DiscID:              "existing-disc",
		OriginalKey:         "existing-key",
		RenderInScreenshots: true,
	}
	slots := []api.ScreenshotSlot{
		decorative,
		existing,
		{RenderInScreenshots: true},
		{OriginalKey: "retained-key", RenderInScreenshots: true},
		{RenderInScreenshots: true},
	}
	secondPath := filepath.Join(dir, "second.png")
	thirdPath := filepath.Join(dir, "third.png")
	attachSelectionPathsToSlots(slots, []api.ScreenshotFinalSelection{
		{
ImagePath: filepath.Join(dir, "first.png"),
 DiscID: "first-disc",
 Order: 0,
},
		{
ImagePath: " " + secondPath + " ",
 DiscID: "second-disc",
 Order: 1,
},
		{
ImagePath: thirdPath,
 DiscID: "third-disc",
 Order: 2,
},
	})
	want := []api.ScreenshotSlot{
		decorative,
		existing,
		{
			ImagePath:           secondPath,
			DiscID:              "second-disc",
			OriginalKey:         secondPath,
			RenderInScreenshots: true,
		},
		{
			ImagePath:           thirdPath,
			DiscID:              "third-disc",
			OriginalKey:         "retained-key",
			RenderInScreenshots: true,
		},
		{RenderInScreenshots: true},
	}
	if !reflect.DeepEqual(slots, want) {
		t.Fatalf("attached selections = %#v, want %#v", slots, want)
	}
}

func TestAttachSelectionPathsToSlotsPreservesSparseOrders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	selections := []api.ScreenshotFinalSelection{
		{ImagePath: filepath.Join(dir, "first.png"), Order: 0},
		{ImagePath: filepath.Join(dir, "third.png"), Order: 2},
	}
	slots := []api.ScreenshotSlot{
		{}, {RenderInScreenshots: true}, {RenderInScreenshots: true}, {}, {RenderInScreenshots: true},
	}
	attachSelectionPathsToSlots(slots, selections)
	if slots[1].ImagePath != selections[0].ImagePath || slots[2].ImagePath != "" || slots[4].ImagePath != selections[1].ImagePath {
		t.Fatal("a later selected image shifted into the gap")
	}
	if selections[0].Order != 0 || selections[1].Order != 2 {
		t.Fatal("selection orders changed")
	}
}

func TestFilterTrackerArtifactSelectionsPreservesUnselectedGaps(t *testing.T) {
	t.Parallel()
	const normalURL = "https://images.example.invalid/normal.png"
	dir := filepath.Join(t.TempDir(), "aither")
	selections := []api.ScreenshotFinalSelection{
		{ImagePath: filepath.Join(dir, "comparison.png"), Order: 0},
		{ImagePath: filepath.Join(dir, buildTrackerArtifactImageName(normalURL, 0)), Order: 2},
	}
	records := []api.TrackerMetadata{{Tracker: "AITHER", ImageURLs: []string{normalURL}}}
	filtered := filterTrackerArtifactSelections(selections, records, descriptionAssetsTestRegistry(t))
	if len(filtered) != 1 || filtered[0].ImagePath != selections[1].ImagePath || filtered[0].Order != 1 {
		t.Fatalf("filtered positions did not retain the unselected gap: %+v", filtered)
	}
	if selections[1].Order != 2 {
		t.Fatal("filter mutated stored selection order")
	}
}
