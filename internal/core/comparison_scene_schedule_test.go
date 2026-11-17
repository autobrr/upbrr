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

func TestComparisonSceneSlotsPreserveGeneralAndOverlappingRequests(t *testing.T) {
	for _, test := range []struct {
		targets [3]int
		count   int
		want    []int
	}{
		{[3]int{1, 2, 1}, 9, []int{0, 1, 2, 1, -1, -1, -1, -1, -1}},
		{[3]int{3, 3, 3}, 4, []int{0, 1, 2, 0}},
		{[3]int{}, 2, []int{-1, -1}},
	} {
		if got := comparisonSceneSlots(test.targets, test.count); !slices.Equal(got, test.want) {
			t.Fatalf("targets=%v count=%d slots=%v want=%v", test.targets, test.count, got, test.want)
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
		{"missing interval", append(slices.Clone(frames[:30]), frames[40:]...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if comparisonIntraOnlyRegion(test.frames, 4, 90) {
				t.Fatal("unobserved coverage authorized an automatic I picture")
			}
		})
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
