// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceOnlySlotVariantDoesNotBlockRehost(t *testing.T) {
	t.Parallel()
	slot := api.ScreenshotSlot{
		OriginalURL:         "https://passthepopcorn.me/static/shot.jpg",
		OriginalHost:        "passthepopcorn.me",
		RenderInScreenshots: true,
		Variants: []api.ScreenshotSlotVariant{{
			Host:       "imgbb",
			UsageScope: "global",
			RawURL:     "https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fshot.png",
		}},
	}
	policy := imageHostPolicy{allowed: []string{"imgbb"}}
	if _, _, _, ok := selectSlotImageForTracker(slot, "ALPHA", policy); ok {
		t.Fatal("source-only slot variant selected as hosted image")
	}
	if allRenderableSlotsHaveEligibleVariant([]api.ScreenshotSlot{slot}, "ALPHA", policy) {
		t.Fatal("source-only variant incorrectly blocked rehost")
	}
}
