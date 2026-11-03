// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

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
		assets trackers.DescriptionAssets
		want   string
	}{
		{
			name:   "multiple screenshots leave one blank line",
			assets: trackers.DescriptionAssets{Description: notes, Screenshots: images},
			want:   notes + "\n\n" + shots,
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
			name: "final description keeps reviewed spacing",
			assets: trackers.DescriptionAssets{
				Description: final,
				Screenshots: images,
				Final:       true,
			},
			want: final,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := prepareDescription(t.Context(), trackers.PreparationInput{Assets: &test.assets})
			if err != nil {
				t.Fatal(err)
			}
			if got.Description != test.want {
				t.Fatalf("description = %q, want %q", got.Description, test.want)
			}
		})
	}
	const rendered = `<img src="https://pixhost.to/1.png" /><br /><br /><img src="https://pixhost.to/2.png" /><br /><br /><img src="https://pixhost.to/3.png" />`
	if got := description.Render(buildScreenshotSection(api.UploadSubject{}, images)); got != rendered {
		t.Fatalf("rendered screenshots = %q, want %q", got, rendered)
	}
	if got := buildDescription(api.UploadSubject{}, config.TrackerConfig{}, config.Config{}, trackers.DescriptionAssets{
		Description: final,
		Screenshots: images,
		Final:       true,
	}); got != final {
		t.Fatalf("upload description changed reviewed spacing: %q", got)
	}
}
