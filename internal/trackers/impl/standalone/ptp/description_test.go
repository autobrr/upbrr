// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func mustBuildDescription(t *testing.T, meta api.UploadSubject, cfg config.TrackerConfig, assets trackers.DescriptionAssets) string {
	t.Helper()
	text, err := buildDescription(meta, cfg, config.Config{}, assets)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestPackScreenshotSectionUsesTwoDistinctFramesPerVideoAndPreservesSpacing(t *testing.T) {
	files := []string{"Movie.cd1.mkv", "Movie.cd2.mkv"}
	first, second := mediafacts.TrackResourceID(files[0]), mediafacts.TrackResourceID(files[1])
	meta := api.UploadSubject{FileList: files, ExactMedia: &api.ExactMediaAssets{}}
	image := func(member, suffix string) api.ScreenshotImage {
		return api.ScreenshotImage{
			PackMemberID: member,
			Host:         "pixhost",
			RawURL:       "https://img.pixhost.to/" + suffix + ".png",
		}
	}
	ordinary := image("", "ordinary")
	pack := []api.ScreenshotImage{image(second, "b1"), image(first, "a1"), image(first, "a2"), image(first, "a3"), image(second, "b2"), ordinary}
	for _, blank := range []bool{false, true} {
		separator := "\n"
		if blank {
			separator += "\n"
		}
		want := make([]string, 0, 4)
		for _, suffix := range []string{"a1", "a2", "b1", "b2"} {
			want = append(want, "[img]https://img.pixhost.to/"+suffix+".png[/img]")
		}
		if got := buildScreenshotSection(meta, pack, blank); got != strings.Join(want, separator) {
			t.Fatalf("pack description with blankLine=%t: %q", blank, got)
		}
	}
	if got := buildScreenshotSection(meta, append(pack[:4:4], ordinary), true); got != "" {
		t.Fatalf("extra frames from one file filled a different member: %q", got)
	}
}

func TestDiscScreenshotSectionCapsExtrasWithoutDroppingFirstDiscFrames(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Discs:      []api.DiscEvidenceResource{{ID: "first"}, {ID: "second"}, {ID: "third"}},
		ExactMedia: &api.ExactMediaAssets{},
	}
	screenshots := make([]api.ScreenshotImage, 0, 4*len(meta.Discs))
	for _, disc := range meta.Discs {
		for _, frame := range []string{"a", "b", "c", "d"} {
			screenshots = append(screenshots, api.ScreenshotImage{
				DiscID: disc.ID,
				Host:   "pixhost",
				RawURL: "https://img.pixhost.to/" + disc.ID + "-" + frame + ".png",
			})
		}
	}
	section := strings.Join(ptpDiscScreenshotGroups(meta.Discs, screenshots, "\n"), "\n")
	if strings.Count(section, "[img]") != 8 || !strings.Contains(section, "first-d.png") ||
		strings.Contains(section, "second-c.png") || strings.Contains(section, "third-c.png") {
		t.Fatalf("wrong per-disc screenshots: %q", section)
	}
	if got := ptpDiscScreenshotGroups(meta.Discs, screenshots[:9], "\n"); got != nil {
		t.Fatalf("other discs supplied missing third-disc frames: %q", got)
	}
}

func TestMultiDVDDescriptionPairsEachReportWithItsOwnImages(t *testing.T) {
	t.Parallel()
	ifo := filepath.Join(t.TempDir(), "second-ifo.txt")
	if err := os.WriteFile(ifo, []byte("DVD B IFO"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		DiscType:   "DVD",
		ExactMedia: &api.ExactMediaAssets{},
		Discs: []api.DiscEvidenceResource{
			{ID: "a", DVDVOBMediaInfoText: "DVD A VOB"},
			{
				ID:                  "b",
				DVDVOBMediaInfoText: "DVD B VOB",
				MediaInfoTextPath:   ifo,
			},
		},
	}
	assets := trackers.DescriptionAssets{Screenshots: []api.ScreenshotImage{
		{
			DiscID: "b",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/b1.png",
		},
		{
			DiscID: "a",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/a1.png",
		},
		{
			DiscID: "b",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/b2.png",
		},
	}}
	want := "[mediainfo]DVD A VOB[/mediainfo]\n[img]https://img.pixhost.to/a1.png[/img]\n\n\n" +
		"[mediainfo]DVD B IFO\n\nDVD B VOB[/mediainfo]\n[img]https://img.pixhost.to/b1.png[/img]\n[img]https://img.pixhost.to/b2.png[/img]"
	if got := mustBuildDescription(t, meta, config.TrackerConfig{ScreenshotBlankLine: new(false)}, assets); got != want {
		t.Fatalf("DVD report and screenshot sequence = %q", got)
	}
}

func TestMultiBluRayDescriptionRequiresEveryPreparedReport(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		DiscType:   "BDMV",
		ExactMedia: &api.ExactMediaAssets{},
		Discs:      []api.DiscEvidenceResource{{ID: "first"}, {ID: "second"}},
		Disc: api.DiscFacts{Items: []api.DiscItemFacts{
			{ID: "first", Reports: []api.DiscReportFacts{{Summary: "PRIMARY REPORT"}}},
			{ID: "second", Reports: []api.DiscReportFacts{{Summary: "SECONDARY REPORT"}}},
		}},
	}
	assets := trackers.DescriptionAssets{Description: "[b]Kept text[/b]", Screenshots: []api.ScreenshotImage{
		{
			DiscID: "first",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/primary.png",
		},
		{
			DiscID: "second",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/second-1.png",
		},
		{
			DiscID: "second",
			Host:   "pixhost",
			RawURL: "https://img.pixhost.to/second-2.png",
		},
	}}
	req := trackers.PreparationInput{Meta: meta, Assets: &assets}
	req.Runtime.Description.SectionOrder = config.DescriptionSectionOrder{"screenshots", "kept"}
	got, err := prepareDescription(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Description, "[mediainfo]") || !strings.HasSuffix(got.Description, "[b]Kept text[/b]") {
		t.Fatalf("gallery and kept text were not reordered: %q", got.Description)
	}
	for _, part := range []string{"PRIMARY REPORT[/mediainfo]\n[img]https://img.pixhost.to/primary.png", "SECONDARY REPORT[/mediainfo]\n[img]https://img.pixhost.to/second-1.png"} {
		if !strings.Contains(got.Description, part) {
			t.Fatalf("description report and gallery do not match: %q", got.Description)
		}
	}
	req.Meta.Disc.Items = req.Meta.Disc.Items[:1]
	got, err = prepareDescription(t.Context(), req)
	if err == nil || got.Description != "" {
		t.Fatalf("published incomplete multi-disc description: %+v, err = %v", got, err)
	}

	assets.Final, assets.Description = true, "[b]Reviewed[/b]"
	got, err = prepareDescription(t.Context(), req)
	if err != nil || got.Description != assets.Description {
		t.Fatalf("reviewed output changed: %+v, err = %v", got, err)
	}
}

func TestPrepareDescriptionScreenshotSpacing(t *testing.T) {
	t.Parallel()
	const notes = "First line\nSecond line\n\n[comparison=Source, Encode]\r\n[center][img]https://img.onlyimage.org/compare.png[/img][/center]\r\n[/comparison]\n\nAfter"
	const shots = "[img]https://pixhost.to/1.png[/img]\n\n[img]https://pixhost.to/2.png[/img]\n\n[img]https://pixhost.to/3.png[/img]"
	const final = "[b]Reviewed[/b]\n[img]https://pixhost.to/1.png[/img]\n[img]https://pixhost.to/2.png[/img]"
	images := []api.ScreenshotImage{
		{Host: "pixhost", RawURL: "https://pixhost.to/1.png"},
		{Host: "pixhost", RawURL: "https://pixhost.to/2.png"},
		{Host: "pixhost", RawURL: "https://pixhost.to/3.png"},
	}
	for _, test := range []struct {
		name   string
		config config.TrackerConfig
		assets trackers.DescriptionAssets
		want   string
	}{
		{
			name:   "multiple screenshots leave one blank line",
			assets: trackers.DescriptionAssets{Description: notes, Screenshots: images},
			want:   notes + "\n\n" + shots,
		},
		{
			name:   "disabled blank line only changes generated screenshots",
			config: config.TrackerConfig{ScreenshotBlankLine: new(false)},
			assets: trackers.DescriptionAssets{Description: notes, Screenshots: images},
			want:   notes + "\n\n" + strings.ReplaceAll(shots, "\n\n", "\n"),
		},
		{
			name:   "single screenshot stays below the minimum",
			assets: trackers.DescriptionAssets{Description: notes, Screenshots: images[:1]},
			want:   notes,
		},
		{
			name:   "empty screenshots add no spacing",
			assets: trackers.DescriptionAssets{Description: notes},
			want:   notes,
		},
		{
			name:   "final description keeps reviewed spacing",
			config: config.TrackerConfig{ScreenshotBlankLine: new(false)},
			assets: trackers.DescriptionAssets{
				Description: final,
				Screenshots: images,
				Final:       true,
			},
			want: final,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := prepareDescription(t.Context(), trackers.PreparationInput{TrackerConfig: test.config, Assets: &test.assets})
			if err != nil {
				t.Fatal(err)
			}
			if got.Description != test.want {
				t.Fatalf("description = %q, want %q", got.Description, test.want)
			}
		})
	}
	const rendered = `<img src="https://pixhost.to/1.png" /><br /><br /><img src="https://pixhost.to/2.png" /><br /><br /><img src="https://pixhost.to/3.png" />`
	for _, blankLine := range []bool{false, true} {
		want := rendered
		if !blankLine {
			want = strings.ReplaceAll(want, "<br /><br />", "<br />")
		}
		if got := description.Render(buildScreenshotSection(api.UploadSubject{}, images, blankLine)); got != want {
			t.Fatalf("rendered screenshots = %q, want %q", got, want)
		}
	}
	if got := mustBuildDescription(t, api.UploadSubject{}, config.TrackerConfig{}, trackers.DescriptionAssets{
		Description: final,
		Screenshots: images,
		Final:       true,
	}); got != final {
		t.Fatalf("upload description changed reviewed spacing: %q", got)
	}
}
