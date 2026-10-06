// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFinalizeDescription(t *testing.T) {
	input := "Hello [user]Alice[/user]!\n\n\n\nCheck this: [img]https://example.invalid/pic.png[/img]"
	got := finalizeDescription(input)

	if strings.Contains(got, "[user]") || strings.Contains(got, "[/user]") {
		t.Fatalf("expected [user] tags stripped, got %q", got)
	}
	if !strings.Contains(got, "Hello Alice!") {
		t.Fatalf("expected username preserved, got %q", got)
	}
	if !strings.Contains(got, "[img width=300]https://example.invalid/pic.png[/img]") {
		t.Fatalf("expected [img width=300] replacement, got %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("expected excess blank lines removed, got %q", got)
	}
}

func TestBuildDescriptionFinalAssets(t *testing.T) {
	assets := trackers.DescriptionAssets{
		Final:       true,
		Description: "  Exact Final Description  ",
	}
	got := buildDescription(trackers.PreparationInput{}, assets)
	if got != "Exact Final Description" {
		t.Fatalf("expected trimmed final description, got %q", got)
	}
}

func TestBuildDescriptionScreenshotsLayout(t *testing.T) {
	meta := api.UploadSubject{
		Options: api.UploadOptions{
			Screens: 4,
		},
	}
	assets := trackers.DescriptionAssets{
		Screenshots: []api.ScreenshotImage{
			{RawURL: "https://example.invalid/1.png", WebURL: "https://example.invalid/view/1"},
			{RawURL: "https://example.invalid/2.png", WebURL: "https://example.invalid/view/2"},
			{RawURL: "https://example.invalid/3.png", WebURL: "https://example.invalid/view/3"},
			{RawURL: "https://example.invalid/4.png", WebURL: "https://example.invalid/view/4"},
		},
	}

	got := buildDescription(trackers.PreparationInput{Meta: meta}, assets)

	if !strings.Contains(got, "[align=center]") || !strings.Contains(got, "[/align]") {
		t.Fatalf("expected screenshot alignment tags, got %q", got)
	}
	if !strings.Contains(got, "[url=https://example.invalid/view/1][img width=350]https://example.invalid/1.png[/img][/url]") {
		t.Fatalf("expected width=350 formatted screenshot, got %q", got)
	}
	// Pairwise: first two separated by space, next separated by double newline
	expectedPair := "[url=https://example.invalid/view/1][img width=350]https://example.invalid/1.png[/img][/url] " +
		"[url=https://example.invalid/view/2][img width=350]https://example.invalid/2.png[/img][/url]\n\n" +
		"[url=https://example.invalid/view/3][img width=350]https://example.invalid/3.png[/img][/url] " +
		"[url=https://example.invalid/view/4][img width=350]https://example.invalid/4.png[/img][/url]"

	if !strings.Contains(got, expectedPair) {
		t.Fatalf("expected pairwise layout %q, got %q", expectedPair, got)
	}
}

func TestBuildDescriptionDiscSectionsAndSignature(t *testing.T) {
	t.Run("DVD VOB MediaInfo", func(t *testing.T) {
		meta := api.UploadSubject{
			DiscType:            "DVD",
			DVDVOBMediaInfoText: "General\nFormat: DVD Video",
		}
		got := buildDescription(trackers.PreparationInput{Meta: meta}, trackers.DescriptionAssets{})
		if !strings.Contains(got, "[spoiler=VOB MediaInfo][code]General\nFormat: DVD Video[/code][/spoiler]") {
			t.Fatalf("expected DVD VOB MediaInfo spoiler, got %q", got)
		}
		if !strings.Contains(got, "[center][url=") || !strings.Contains(got, "upbrr") {
			t.Fatalf("expected upbrr signature link, got %q", got)
		}
	})

	t.Run("BDMV BDINFO", func(t *testing.T) {
		meta := api.UploadSubject{
			DiscType: "BDMV",
			Disc: api.DiscFacts{
				Summary: "DISC INFO:\nDisc Title: Sample Blu-ray",
			},
		}
		got := buildDescription(trackers.PreparationInput{Meta: meta}, trackers.DescriptionAssets{})
		if !strings.Contains(got, "[spoiler=BDINFO][code]DISC INFO:\nDisc Title: Sample Blu-ray[/code][/spoiler]") {
			t.Fatalf("expected BDMV BDINFO spoiler, got %q", got)
		}
	})
}

func TestBuildDescriptionCustomHeaderAndEpisodeOverview(t *testing.T) {
	meta := api.UploadSubject{
		EpisodeTitle:    "Pilot",
		EpisodeOverview: "The journey begins.",
	}
	runtime := trackers.PreparationRuntime{
		Description: config.DescriptionSettingsConfig{
			CustomDescriptionHeader: "[b]Custom Header[/b]",
		},
	}

	got := buildDescription(trackers.PreparationInput{
		Meta:    meta,
		Runtime: runtime,
	}, trackers.DescriptionAssets{})

	if !strings.Contains(got, "[b]Custom Header[/b]") {
		t.Fatalf("expected custom header in description, got %q", got)
	}
	if !strings.Contains(got, "[center]Pilot[/center]") {
		t.Fatalf("expected centered episode title, got %q", got)
	}
	if !strings.Contains(got, "[center]The journey begins.[/center]") {
		t.Fatalf("expected centered episode overview, got %q", got)
	}
}

func TestPrepareDescription(t *testing.T) {
	req := trackers.PreparationInput{
		Meta: api.UploadSubject{
			EpisodeTitle: "Episode 1",
		},
	}
	res, err := prepareDescription(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected prepareDescription error: %v", err)
	}
	if res.Group != "fld" {
		t.Fatalf("expected group fld, got %q", res.Group)
	}
	if !strings.Contains(res.Description, "upbrr") {
		t.Fatalf("expected upbrr signature in description, got %q", res.Description)
	}
}
