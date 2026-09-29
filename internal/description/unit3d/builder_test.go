// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionKeepsComparisonImagesWhenReplacingScreenshots(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[align=left]Source &amp; Encode[/align]\r\n\r\n\r\n" +
		"[center][url=https://img.example/comparison-page][img width=320]https://img.example/comparison.png[/img][/url][/center]\r\n[/spoiler]"
	kept := "Release notes\n\n" + comparison + "\n\n[center][img]https://img.example/old-screen.png[/img][/center]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, kept, nil, []api.ScreenshotImage{{ImgURL: "https://img.example/new-screen.png"}})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) {
		t.Fatalf("comparison block was changed or removed: %q", description)
	}
	if strings.Contains(description, "old-screen.png") || !strings.Contains(description, "new-screen.png") {
		t.Fatalf("screenshot replacement failed: %q", description)
	}
}

func TestBuildDescriptionDoesNotDeduplicateSelectedScreenshotAgainstComparison(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	comparison := "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, comparison, nil, []api.ScreenshotImage{{ImgURL: imageURL}})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) || strings.Count(description, imageURL) != 2 {
		t.Fatalf("comparison hid separately selected screenshot: %q", description)
	}
}

func TestBuildDescriptionKeepsNestedSourceAudioInComparison(t *testing.T) {
	comparison := "[spoiler=Comparisons]\n[spoiler=source_audio]copied audio[/spoiler]\n[/spoiler]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, "Notes\n\n"+comparison, nil, nil)
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) {
		t.Fatalf("nested comparison changed: %q", description)
	}
}

func TestBuildDescriptionPlacesAudioAfterMenusBeforeScreenshots(t *testing.T) {
	t.Parallel()
	audio := "[spoiler=source_audio]\n[img]https://img.example/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]"
	cfg := config.Config{}
	cfg.Description.ThumbnailSize = 420
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, cfg,
		config.TrackerConfig{}, api.NopLogger{}, "Base description\n\n"+audio,
		[]api.ScreenshotImage{{ImgURL: "https://img.example/menu.png"}},
		[]api.ScreenshotImage{{ImgURL: "https://img.example/screen.png"}})
	if err != nil {
		t.Fatal(err)
	}
	basePos := strings.Index(description, "Base description")
	menuPos := strings.Index(description, "https://img.example/menu.png")
	audioPos := strings.Index(description, "https://img.example/audio.png")
	screenPos := strings.Index(description, "https://img.example/screen.png")
	if basePos < 0 || menuPos <= basePos || audioPos <= menuPos || screenPos <= audioPos ||
		!strings.Contains(description, "[spoiler=source_audio]") ||
		!strings.Contains(description, "[img=420]https://img.example/audio.png[/img]") ||
		!strings.Contains(description, "[img=420]https://img.example/screen.png[/img]") ||
		!strings.Contains(description, "[code]Peak: -1 dB[/code]") {
		t.Fatalf("audio analysis placement = %q", description)
	}
}

func TestBuildDiscScreenshotSectionsGroupsPreparedDiscOrder(t *testing.T) {
	t.Parallel()

	meta := api.DescriptionSubject{Disc: api.DiscFacts{Items: []api.DiscItemFacts{
		{
			ID:   "disc-one",
			Name: "Disc 1",
			Type: "BDMV",
		},
		{
			ID:   "disc-two",
			Name: "Disc 2",
			Type: "BDMV",
		},
	}}}
	images := []api.ScreenshotImage{
		{
			DiscID: "disc-two",
			RawURL: "https://images.example.invalid/disc-two.png",
		},
		{
			DiscID: "disc-one",
			RawURL: "https://images.example.invalid/disc-one.png",
		},
	}

	section := buildDiscScreenshotSections(meta, images, 350, 2)
	wantOrder := []string{"[b]Disc 1[/b]", "disc-one.png", "[b]Disc 2[/b]", "disc-two.png"}
	previous := -1
	for _, value := range wantOrder {
		index := strings.Index(section, value)
		if index <= previous {
			t.Fatalf("section order for %q in %q", value, section)
		}
		previous = index
	}
}

func TestDVDVOBMediaInfoBlockIncludesEveryDiscOnce(t *testing.T) {
	t.Parallel()

	meta := api.DescriptionSubject{
		DiscType: "DVD",
		Discs: []api.DiscEvidenceResource{
			{
				ID:                  "disc-one",
				Name:                "Disc 1",
				Type:                "DVD",
				DVDVOBMediaInfoText: "VOB INFO ONE",
			},
			{
				ID:                  "disc-two",
				Name:                "Disc 2",
				Type:                "DVD",
				DVDVOBMediaInfoText: "VOB INFO TWO",
			},
		},
		DVDVOBMediaInfoText: "VOB INFO ONE",
	}
	block := DVDVOBMediaInfoBlock(meta)
	if strings.Count(block, "VOB INFO ONE") != 1 || strings.Count(block, "VOB INFO TWO") != 1 {
		t.Fatalf("DVD VOB block = %q", block)
	}
	if strings.Index(block, "Disc 1") >= strings.Index(block, "Disc 2") {
		t.Fatalf("DVD VOB block order = %q", block)
	}
}
