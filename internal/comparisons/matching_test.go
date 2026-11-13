// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func sampleFrame(seed uint64, seconds float64, kind string) FrameSample {
	rng := rand.New(rand.NewPCG(seed, 37))
	luma := make([]byte, 64)
	for i := range luma {
		luma[i] = byte(32 + rng.IntN(192))
	}
	return FrameSample{
		TimestampSeconds: seconds,
		FrameType:        kind,
		Luma:             luma,
		Brightness:       .5,
	}
}

func TestMatchingFollowsContentAcrossTimingEdits(t *testing.T) {
	main := SampledSource{ID: "release"}
	other := SampledSource{ID: "source", Anchors: []MatchAnchor{{MainSeconds: 1, SourceSeconds: 4}}}
	for i := range 8 {
		main.Frames = append(main.Frames, sampleFrame(uint64(i+1), float64(i+1), "I"))
		// Intro, different speed/FPS and an inserted scene invalidate one global offset.
		seconds := 3 + float64(i+1)*1.04
		if i >= 4 {
			seconds += 2
		}
		if i != 5 {
			other.Frames = append(other.Frames, sampleFrame(uint64(i+1), seconds, "B"))
		}
	}
	other.Frames = append(other.Frames, sampleFrame(81, 8.5, "P"))
	slices.SortFunc(other.Frames, func(a, b FrameSample) int { return cmp.Compare(a.TimestampSeconds, b.TimestampSeconds) })
	result, err := Match([]SampledSource{main, other}, "release", MatchOptions{MainMoments: []float64{2, 5, 6, 8}})
	if err != nil || len(result.Rows) != 4 {
		t.Fatalf("rows=%+v err=%v", result, err)
	}
	for i, want := range []float64{5.08, 10.2, 0, 13.32} {
		row := result.Rows[i]
		if i == 2 {
			if row.Complete || row.Frames[1].Resolved || row.Frames[1].Reason != "no_visual_match" {
				t.Fatalf("removed scene matched: %+v", row)
			}
		} else if !row.Complete || math.Abs(row.Frames[1].TimestampSeconds-want) > 1e-6 {
			t.Fatalf("content after timing edit: %+v, want %g", row, want)
		}
	}
}

func TestMatchingAmbiguityAnchorsAndPictureTypes(t *testing.T) {
	main := SampledSource{
		ID:           "main",
		Frames:       []FrameSample{sampleFrame(1, 1, "I")},
		AllowedTypes: []string{"I"},
	}
	for _, tc := range []struct {
		name     string
		frames   []FrameSample
		anchors  []MatchAnchor
		resolved bool
		fallback bool
		seconds  float64
	}{
		{"nearby allowed type", []FrameSample{sampleFrame(1, 4, "P"), sampleFrame(1, 4.04, "B")}, nil, true, false, 4.04},
		{"type fallback", []FrameSample{sampleFrame(1, 4, "P"), sampleFrame(2, 4.04, "B")}, nil, true, true, 4},
		{"distant repeated scene", []FrameSample{sampleFrame(1, 4, "P"), sampleFrame(1, 20, "B")}, nil, false, false, 4},
		{"manual resolves ambiguity", []FrameSample{sampleFrame(1, 4, "P"), sampleFrame(1, 20, "B")}, []MatchAnchor{{MainSeconds: 1, SourceSeconds: 20}}, true, false, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Match([]SampledSource{main, {
				ID:           "encode",
				Frames:       tc.frames,
				AllowedTypes: []string{"B"},
				Anchors:      tc.anchors,
			}}, "main", MatchOptions{MainMoments: []float64{1}})
			if err != nil || len(result.Rows) != 1 || len(result.Rows[0].Frames) != 2 {
				t.Fatalf("missing comparison row: %+v %v", result, err)
			}
			match := result.Rows[0].Frames[1]
			if match.Resolved != tc.resolved || match.TypeFallback != tc.fallback || math.Abs(match.TimestampSeconds-tc.seconds) > 1e-6 {
				t.Fatalf("match=%+v", match)
			}
			if !tc.resolved && match.Reason != "ambiguous_visual_match" {
				t.Fatalf("ambiguity lost: %+v", match)
			}
			if len(tc.anchors) > 0 && (match.Method != "manual" || match.Confidence != 0) {
				t.Fatalf("manual evidence invented confidence: %+v", match)
			}
		})
	}
	for _, allowed := range [][]string{{"P"}, {"I", "P"}, {"I", "P", "B"}} {
		result, err := Match([]SampledSource{main, {
			ID:           "encode",
			Frames:       []FrameSample{sampleFrame(1, 4, "P")},
			AllowedTypes: allowed,
		}}, "main", MatchOptions{MainMoments: []float64{1}})
		if err != nil || len(result.Rows) != 1 || !result.Rows[0].Complete || result.Rows[0].Frames[1].TypeFallback || result.Rows[0].Frames[1].FrameType != "P" {
			t.Fatalf("allowed=%v result=%+v error=%v", allowed, result, err)
		}
	}
}

func TestMatchingDoesNotReuseAnAutomaticTargetFrame(t *testing.T) {
	main := SampledSource{ID: "main", Frames: []FrameSample{sampleFrame(1, 1, "I"), sampleFrame(1, 20, "I")}}
	other := SampledSource{ID: "other", Frames: []FrameSample{sampleFrame(1, 4, "I")}}
	result, err := Match([]SampledSource{main, other}, "main", MatchOptions{Count: 2})
	if err != nil || len(result.Rows) != 2 {
		t.Fatalf("matching failed: %+v %v", result, err)
	}
	for _, row := range result.Rows {
		if row.Complete || row.Frames[1].Resolved || row.Frames[1].Reason != "ambiguous_visual_match" {
			t.Fatalf("one target occurrence resolved distinct main moments: %+v", row)
		}
	}
	main.Frames = slices.Insert(main.Frames, 1, sampleFrame(2, 10, "I"))
	other.Frames = append(other.Frames, sampleFrame(2, 10, "I"))
	result, err = Match([]SampledSource{main, other}, "main", MatchOptions{Count: 2})
	if err != nil || !slices.ContainsFunc(result.Rows, func(row MatchedRow) bool { return row.Complete && row.MainSeconds == 10 }) {
		t.Fatalf("ambiguous scenes prevented a different common moment: %+v %v", result, err)
	}
	other.Anchors = []MatchAnchor{{MainSeconds: 1, SourceSeconds: 4}, {MainSeconds: 20, SourceSeconds: 4}}
	result, err = Match([]SampledSource{main, other}, "main", MatchOptions{MainMoments: []float64{1, 20}})
	if err != nil || len(result.Rows) != 2 || !result.Rows[0].Complete || !result.Rows[1].Complete {
		t.Fatalf("explicit correspondence did not resolve ambiguity: %+v %v", result, err)
	}
}

func TestMatchingSceneMixExcludesBlankAndReportsUnmet(t *testing.T) {
	source := SampledSource{ID: "main"}
	for i := range 9 {
		source.Frames = append(source.Frames, sampleFrame(uint64(i+1), float64(i), "I"))
	}
	source.Frames[1].Luma = make([]byte, 64)
	source.Frames[1].Brightness = 0
	source.Frames[3].Brightness = .15
	source.Frames[5].Motion = .8
	other := source
	other.ID = "other"
	result, err := Match([]SampledSource{source, other}, "main", MatchOptions{
		Count:          3,
		DarkFraction:   1.0 / 3,
		MotionFraction: 1.0 / 3,
	})
	if err != nil || len(result.Rows) != 3 || result.AchievedDark < 1 || result.AchievedMotion < 1 {
		t.Fatalf("mix=%+v err=%v", result, err)
	}
	for _, row := range result.Rows {
		if row.MainSeconds == 1 || !row.Complete {
			t.Fatalf("invalid automatic scene: %+v", row)
		}
	}
	result, err = Match([]SampledSource{source, other}, "main", MatchOptions{Count: 3, DarkFraction: 1})
	if err != nil || result.AchievedDark != 1 || len(result.Warnings) == 0 {
		t.Fatalf("unmet mix not reported: %+v %v", result, err)
	}
}
