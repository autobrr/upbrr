// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

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
