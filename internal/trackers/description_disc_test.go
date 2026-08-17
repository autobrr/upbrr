// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestOrderDescriptionMediaUsesPreparedDiscOrder(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Disc: api.DiscFacts{Items: []api.DiscItemFacts{
		{
			ID:   "disc-one",
			Name: "Disc 1",
			Type: "DVD",
		},
		{
			ID:   "disc-two",
			Name: "Disc 2",
			Type: "DVD",
		},
	}}}
	images := []api.ScreenshotImage{
		{DiscID: "disc-two", Path: "disc-two.png"},
		{DiscID: "", Path: "manual.png"},
		{DiscID: "disc-one", Path: "disc-one.png"},
	}

	ordered := orderDescriptionMedia(meta, images)
	if ordered[0].Path != "disc-one.png" || ordered[0].DiscName != "Disc 1" ||
		ordered[1].Path != "disc-two.png" || ordered[1].DiscName != "Disc 2" ||
		ordered[2].Path != "manual.png" {
		t.Fatalf("ordered media = %#v", ordered)
	}
	if images[0].DiscName != "" {
		t.Fatal("source images were mutated")
	}
}
