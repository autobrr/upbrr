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
			TimeBaseNumerator:        1,
			TimeBaseDenominator:      1000,
			DurationTicks:            1000,
			RelativeTimestampSeconds: float64(i),
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
			frames, missing, err := cache.get(1, test.start, test.end, test.mode)
			if err != nil || !reflect.DeepEqual(frames, test.want) || !reflect.DeepEqual(missing, test.missing) {
				t.Fatalf("cached native frames=%+v gaps=%+v", frames, missing)
			}
		})
	}
	t.Run("all compatible intervals and a missing native span", func(t *testing.T) {
		c := comparisonWindowCache{}
		c.put(1, 10, 16, "none", native[:2], 6*4096)
		c.put(1, 10, 16, "none", native[4:], 6*4096)
		frames, gaps, err := c.get(1, 10, 16, "none")
		if err != nil || len(frames) != 4 || len(gaps) != 1 || gaps[0] != (api.ComparisonTimeInterval{Start: 11, End: 14}) {
			t.Fatalf("missing native gap: frames=%d gaps=%v", len(frames), gaps)
		}
	})
	t.Run("three short gaps preserve cached work", func(t *testing.T) {
		c := comparisonWindowCache{}
		for _, i := range []int{0, 2, 4} {
			c.put(1, 10, 16, "none", native[i:i+1], 6*4096)
		}
		hits, gaps, err := c.get(1, 10, 16, "none")
		if err != nil || len(hits) != 3 || len(gaps) != 3 {
			t.Fatalf("discarded useful intervals: hits=%d gaps=%v", len(hits), gaps)
		}
	})
	t.Run("separate reads leave a missing coarse-clock picture", func(t *testing.T) {
		archive := make([]screenshots.ComparisonSample, 0, 2)
		for _, pts := range []int64{100, 102} {
			at := float64(pts) / 25
			archive = append(archive, screenshots.ComparisonSample{
				NativeSequence:           true,
				PTS:                      pts,
				TimeBaseNumerator:        1,
				TimeBaseDenominator:      25,
				DurationTicks:            1,
				RelativeTimestampSeconds: at,
				Replay:                   &screenshots.ComparisonFrameReplay{Version: 2, File: &screenshots.ComparisonFileInterval{Start: at, Duration: .04}},
			})
		}
		c := comparisonWindowCache{}
		c.put(0, 4, 4.12, "none", archive, 16<<20)
		_, gaps, err := c.get(0, 4, 4.12, "none")
		if err != nil || len(gaps) != 1 || gaps[0].Start > 4.04 || gaps[0].End < 4.08 {
			t.Fatalf("missing native picture was treated as cached: %v", gaps)
		}
	})
	t.Run("fractional cadence across a shared physical boundary", func(t *testing.T) {
		a, b := native[0], native[1]
		a.PTS, b.PTS = 1000, 1042
		a.RelativeTimestampSeconds, b.RelativeTimestampSeconds = 1, 1.042
		a.DurationTicks, b.DurationTicks = 41, 41
		a.Replay = &screenshots.ComparisonFrameReplay{Version: 2, File: &screenshots.ComparisonFileInterval{
			Start:          0,
			Duration:       2,
			PacketPosition: new(int64(80)),
		}}
		b.Replay = &screenshots.ComparisonFrameReplay{Version: 2, File: &screenshots.ComparisonFileInterval{
			Start:          1,
			Duration:       2,
			PacketPosition: new(int64(92)),
		}}
		if comparisonRoundedNativeBoundary(a, b) {
			t.Fatal("unconnected intervals gave rounding authority")
		}
		b.Replay.File.Previous = &screenshots.ComparisonFileNeighbor{PTS: a.PTS, Position: 80}
		if !comparisonRoundedNativeBoundary(a, b) {
			t.Fatal("a shared packet lost native cadence at the window boundary")
		}
		for _, frame := range []*screenshots.ComparisonSample{&a, &b} {
			frame.Evidence = &api.ComparisonSceneEvidence{Eligible: true}
		}
		c := comparisonWindowCache{}
		c.put(0, 1, 1.083, "none", []screenshots.ComparisonSample{a}, 16<<20)
		c.put(0, 1, 1.083, "none", []screenshots.ComparisonSample{b}, 16<<20)
		frames, gaps, err := c.get(0, 1, 1.083, "none")
		if err != nil || len(frames) != 2 || len(gaps) != 0 {
			t.Fatalf("verified rounding scheduled an extra decode: frames=%d gaps=%v err=%v", len(frames), gaps, err)
		}
		if section := comparisonEligibleSection(frames, a.FrameID); len(section) != 2 {
			t.Fatal("verified rounding ended the observed scene")
		}
		b.PTS, b.RelativeTimestampSeconds = 1083, 1.083
		if section := comparisonEligibleSection([]screenshots.ComparisonSample{a, b}, a.FrameID); len(section) != 1 {
			t.Fatal("scene continued across a missing native picture")
		}
	})
	t.Run("merge keeps the enclosed address and the first classification", func(t *testing.T) {
		first := native[0]
		first.Replay = &screenshots.ComparisonFrameReplay{Version: 2, File: &screenshots.ComparisonFileInterval{
			Start:          9,
			Duration:       2,
			Unenclosed:     true,
			PacketPosition: new(int64(123)),
		}}
		first.Evidence = &api.ComparisonSceneEvidence{Eligible: true}
		overlap := first
		overlap.Replay = &screenshots.ComparisonFrameReplay{Version: 2, File: &screenshots.ComparisonFileInterval{
			Start:          8,
			Duration:       4,
			PacketPosition: new(int64(123)),
		}}
		overlap.Evidence = nil
		merged, err := orderedComparisonSamples([]screenshots.ComparisonSample{first, overlap})
		if err != nil || len(merged) != 1 || merged[0].Replay != overlap.Replay || merged[0].Evidence != first.Evidence {
			t.Fatalf("overlap lost its proved locator or classification: %v", err)
		}
	})
	t.Run("underestimated VFR read reclaims optional memory as observations arrive", func(t *testing.T) {
		c := comparisonWindowCache{}
		for i := range 5 {
			c.retain(comparisonCachedWindow{source: i, frames: make([]screenshots.ComparisonSample, 256)}, 5<<20)
		}
		admit := c.reserve(6 << 20)
		// Eight seconds reported at 24 fps actually deliver 480 native pictures.
		for i := 1; i <= 480; i++ {
			if !admit(int64(i) * 4096) {
				t.Fatal("cache blocked an affordable native read")
			}
		}
		if c.bytes != 4<<20 || c.evictions != 1 || admit((6<<20)+1) {
			t.Fatalf("wrong working/cache limit: retained=%d evictions=%d", c.bytes, c.evictions)
		}
	})
	cache.put(2, 10, 16, "none", native, 6*4096)
	if frames, _, _ := cache.get(1, 10, 16, "none"); len(frames) != 0 || cache.bytes != 6*4096 {
		t.Fatal("cache exceeded its allowance instead of evicting old windows")
	}
	cache.trim(4096)
	if cache.bytes != 0 || len(cache.windows) != 0 {
		t.Fatal("shrinking the parent allowance retained decoded rasters")
	}
	for _, change := range []string{"none", "duration", "picture type", "native clock"} {
		t.Run("shared boundary "+change, func(t *testing.T) {
			boundary := native[len(native)-1]
			switch change {
			case "duration":
				boundary.DurationTicks++
			case "picture type":
				boundary.PictureType = "I"
			case "native clock":
				boundary.TimeBaseDenominator++
			}
			merged, err := orderedComparisonSamples(append(append([]screenshots.ComparisonSample(nil), native...), boundary))
			if change == "none" && (err != nil || !reflect.DeepEqual(merged, native)) || change != "none" && err == nil {
				t.Fatalf("shared native boundary was not verified before deduplication: %v", err)
			}
		})
	}
}
