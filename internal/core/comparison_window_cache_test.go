// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonSecondaryWindowCacheRetainsNativeFramesWithinBudget(t *testing.T) {
	var native []screenshots.ComparisonSample
	for i := 10; i < 16; i++ {
		native = append(native, screenshots.ComparisonSample{
			NativeSequence:           true,
			FrameID:                  fmt.Sprintf("picture-%d", i),
			PTS:                      int64(i * 1000),
			RelativeTimestampSeconds: float64(i),
			PictureChecksum:          "original",
			Pixels:                   []byte{byte(i)},
		})
	}
	cache := comparisonWindowCache{}
	// A seek edge just beyond a native timestamp must not hide the next
	// picture when the decoder rounds that edge to nanoseconds.
	cache.put(1, 10, 16+1e-10, "none", native, 6*4096)
	for _, test := range []struct {
		name       string
		start, end float64
		mode       string
		want       []screenshots.ComparisonSample
		missing    []api.ComparisonTimeInterval
	}{
		{"contained", 11, 15, "none", native[1:5], nil},
		{"right overlap", 14, 18, "none", native[4:], []api.ComparisonTimeInterval{{Start: 15, End: 18}}},
		{"both edges", 9, 17, "none", native, []api.ComparisonTimeInterval{{Start: 9, End: 10}, {Start: 15, End: 17}}},
		{"renderer changed", 11, 15, "software", nil, []api.ComparisonTimeInterval{{Start: 11, End: 15}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames, missing := cache.get(1, test.start, test.end, test.mode)
			if !reflect.DeepEqual(frames, test.want) || !reflect.DeepEqual(missing, test.missing) {
				t.Fatalf("cached native frames=%+v gaps=%+v", frames, missing)
			}
		})
	}
	cache.put(2, 10, 16, "none", native, 6*4096)
	if frames, _ := cache.get(1, 10, 16, "none"); len(frames) != 0 || cache.bytes != 6*4096 {
		t.Fatal("cache exceeded its allowance instead of evicting old windows")
	}
	cache.trim(4096)
	if cache.bytes != 0 || len(cache.windows) != 0 {
		t.Fatal("shrinking the parent allowance retained decoded rasters")
	}
	for _, change := range []string{"none", "checksum", "picture type", "pixel format"} {
		t.Run("shared boundary "+change, func(t *testing.T) {
			boundary := native[len(native)-1]
			switch change {
			case "checksum":
				boundary.PictureChecksum = "different"
			case "picture type":
				boundary.PictureType = "I"
			case "pixel format":
				boundary.PixelFormat = "yuv420p10le"
			}
			merged, err := orderedComparisonSamples(append(append([]screenshots.ComparisonSample(nil), native...), boundary))
			if change == "none" && (err != nil || !reflect.DeepEqual(merged, native)) || change != "none" && err == nil {
				t.Fatalf("shared native boundary was not verified before deduplication: %v", err)
			}
		})
	}
}
