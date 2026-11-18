// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonSceneScheduleUsesMeasuredOverlappingAchievements(t *testing.T) {
	for _, test := range []struct {
		targets, achieved, attempts [3]int
		want                        int
	}{
		{[3]int{1, 2, 1}, [3]int{}, [3]int{}, 0},
		// One verified row satisfies motion and dark; the next slot retargets flat.
		{[3]int{1, 2, 1}, [3]int{1, 1, 0}, [3]int{1, 0, 0}, 2},
		{[3]int{1, 2, 1}, [3]int{1, 1, 1}, [3]int{1, 0, 1}, 1},
		{[3]int{3, 3, 3}, [3]int{1, 1, 1}, [3]int{}, 0},
		{[3]int{1, 2, 1}, [3]int{}, [3]int{10, 0, 0}, 1},
		{[3]int{1, 2, 1}, [3]int{}, [3]int{10, 20, 0}, 2},
		{[3]int{1, 2, 1}, [3]int{}, [3]int{10, 20, 10}, -1},
		{[3]int{1, 2, 1}, [3]int{1, 2, 1}, [3]int{}, -1},
		{[3]int{}, [3]int{}, [3]int{}, -1},
	} {
		if got := comparisonSceneCategory(test.targets, test.achieved, test.attempts); got != test.want {
			t.Fatalf("targets=%v achieved=%v attempts=%v next=%d want=%d", test.targets, test.achieved, test.attempts, got, test.want)
		}
	}
}

func TestComparisonIntraOnlyRegionRequiresUniqueNativeCoverage(t *testing.T) {
	frames := make([]screenshots.ComparisonSample, 0, 90)
	for i := range 90 {
		identity := api.ComparisonFrameIdentity{
			Version:             1,
			InputFingerprint:    "source",
			Occurrence:          "file",
			StreamID:            "video",
			PTS:                 int64(i),
			TimeBaseNumerator:   1,
			TimeBaseDenominator: 10,
		}
		frames = append(frames, screenshots.ComparisonSample{
			NativeSequence:           true,
			FrameID:                  fmt.Sprintf("frame-%d", i),
			Identity:                 &identity,
			Replay:                   &screenshots.ComparisonFrameReplay{Identity: identity},
			PictureType:              "I",
			RelativeTimestampSeconds: float64(i) / 10,
			DurationTicks:            1,
			TimeBaseNumerator:        1,
			TimeBaseDenominator:      10,
		})
	}
	if !comparisonIntraOnlyRegion(frames, 4, 90) {
		t.Fatal("a substantial native all-I region could not complete a row")
	}
	t.Run("independent file reads need a physical bridge", func(t *testing.T) {
		fileFrames := slices.Clone(frames)
		for i := range fileFrames {
			f := &fileFrames[i]
			f.PTS = f.Identity.PTS
			f.Replay = &screenshots.ComparisonFrameReplay{
				Identity: *f.Identity,
				Version:  2,
				File: &screenshots.ComparisonFileInterval{
					Start:          float64(i/45) * 4.5,
					Duration:       4.5,
					Slot:           i % 45,
					PacketPosition: new(int64(i + (i/45)*1000)),
				},
			}
		}
		for i, f := range fileFrames {
			if i%45 > 0 {
				v := fileFrames[i-1]
				f.Replay.File.Previous = &screenshots.ComparisonFileNeighbor{PTS: v.PTS, Position: *v.Replay.File.PacketPosition}
			}
			if i%45 < 44 {
				v := fileFrames[i+1]
				f.Replay.File.Next = &screenshots.ComparisonFileNeighbor{PTS: v.PTS, Position: *v.Replay.File.PacketPosition}
			}
		}
		cache := comparisonWindowCache{}
		cache.put(0, 0, 4.5, "none", fileFrames[:45], 16<<20)
		cache.put(0, 4.5, 9, "none", fileFrames[45:], 16<<20)
		hits, gaps, err := cache.get(0, 0, 9, "none")
		if err != nil || len(gaps) != 1 || gaps[0].Start > 4.4 || gaps[0].End <= 4.5 || comparisonIntraOnlyRegion(hits, 4, 90) || verifyComparisonFileJoins(hits) == nil {
			t.Fatalf("abutting clocks joined different stretches: %v %v", gaps, err)
		}
		bridge := slices.Clone(fileFrames[44:46])
		for i := range bridge {
			replay := cloneComparisonWitness(*bridge[i].Replay)
			replay.File.Start, replay.File.Duration, replay.File.Slot = 4.4, .2, i
			bridge[i].Replay = &replay
		}
		bridge[0].Replay.File.Next = &screenshots.ComparisonFileNeighbor{PTS: bridge[1].PTS, Position: *bridge[1].Replay.File.PacketPosition}
		bridge[1].Replay.File.Previous = &screenshots.ComparisonFileNeighbor{PTS: bridge[0].PTS, Position: *bridge[0].Replay.File.PacketPosition}
		joined, err := orderedComparisonSamples(append(slices.Clone(hits), bridge...))
		if err != nil || verifyComparisonFileJoins(joined) != nil || !comparisonIntraOnlyRegion(joined, 4, 90) {
			t.Fatalf("lost an observed shared packet at the bridge: %v", err)
		}
		bridge[1].Replay.File.PacketPosition = new(int64(9999))
		selectable := comparisonManualTargetSamples(fileFrames[43:46], bridge)
		if !selectable[0].Unselectable || selectable[1].Unselectable || !selectable[2].Unselectable {
			t.Fatal("manual all-I policy did not restrict selection to the common physical picture")
		}
		if _, err = orderedComparisonSamples(append(hits, bridge...)); err == nil {
			t.Fatal("conflicting bridge packet was accepted")
		}
	})
	for i := 45; i < len(frames); i++ {
		identity := *frames[i].Identity
		identity.Occurrence, identity.PTS = "next-cell", int64(i-45)
		frames[i].Identity = &identity
		frames[i].Replay = &screenshots.ComparisonFrameReplay{Identity: identity}
	}
	if !comparisonIntraOnlyRegion(frames, 4, 90) {
		t.Fatal("adjacent all-I authored cells lost their observed type coverage")
	}
	for _, test := range []struct {
		name   string
		frames []screenshots.ComparisonSample
	}{
		{"short", frames[:40]},
		{"overlapping reads", append(slices.Clone(frames[:40]), frames[:40]...)},
		{"one missing native tick", append(slices.Clone(frames[:30]), frames[31:]...)},
		{"missing interval", append(slices.Clone(frames[:30]), frames[40:]...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if comparisonIntraOnlyRegion(test.frames, 4, 90) {
				t.Fatal("unobserved coverage authorized an automatic I picture")
			}
		})
	}
	if comparisonIntraOnlyRegion(frames[1:40], 2, 4) || comparisonIntraOnlyRegion(frames[:39], 2, 4) {
		t.Fatal("missing coarse-clock edge was accepted as whole-source coverage")
	}
	if !comparisonIntraOnlyRegion(frames[:40], 2, 4) {
		t.Fatal("a fully observed short I-only source could not complete")
	}
	frames[45].PictureType = "P"
	if comparisonIntraOnlyRegion(frames, 4, 90) {
		t.Fatal("a local B/P alternative authorized I fallback")
	}
	frames[45].PictureType = "I"
	if comparisonIntraOnlyRegion(frames, 12, 90) {
		t.Fatal("an unrelated region authorized this row's I fallback")
	}
}
