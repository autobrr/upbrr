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

func TestMatchingDefaultPicturePolicy(t *testing.T) {
	main := SampledSource{
		ID:            "main",
		PreferBFrames: true,
		AllowedTypes:  []string{"B"},
		Frames: []FrameSample{
			sampleFrame(1, 1, "I"), sampleFrame(2, 10, "P"), sampleFrame(3, 20, "B"),
		},
	}
	other := main
	other.ID = "other"
	for count := 1; count <= 3; count++ {
		result, err := Match([]SampledSource{main, other}, "main", MatchOptions{Count: count})
		if err != nil || len(result.Rows) != count {
			t.Fatalf("count=%d rows=%+v error=%v", count, result.Rows, err)
		}
		types := make([]string, 0, len(result.Rows))
		for _, row := range result.Rows {
			if !row.Complete {
				t.Fatalf("default fallback did not complete a suitable scene: %+v", row)
			}
			types = append(types, row.Frames[0].FrameType)
		}
		slices.Sort(types)
		want := []string{"B", "P", "I"}[:count]
		want = slices.Clone(want)
		slices.Sort(want)
		if !slices.Equal(types, want) {
			t.Fatalf("count=%d selected=%v want=%v", count, types, want)
		}
	}
	// A preferred B picture without correspondence must not prevent a useful P scene.
	other.Frames = other.Frames[:2]
	fallback, err := Match([]SampledSource{main, other}, "main", MatchOptions{Count: 1})
	if err != nil || len(fallback.Rows) != 1 || !fallback.Rows[0].Complete || fallback.Rows[0].Frames[0].FrameType != "P" {
		t.Fatalf("unmatched B prevented P fallback: %+v %v", fallback.Rows, err)
	}
	for _, tc := range []struct {
		name      string
		mainType  string
		otherType string
		explicit  bool
		manual    bool
		resolved  bool
	}{
		{"B main permits P fallback", "B", "P", false, false, true},
		{"B main rejects I fallback", "B", "I", false, false, false},
		{"P main rejects I fallback", "P", "I", false, false, false},
		{"I main permits I fallback", "I", "I", false, false, true},
		{"explicit I remains selected", "B", "I", true, false, true},
		{"manual I remains selected", "B", "I", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := SampledSource{ID: "main", Frames: []FrameSample{sampleFrame(1, 1, tc.mainType)}}
			other := SampledSource{
				ID:            "other",
				PreferBFrames: !tc.explicit,
				AllowedTypes:  []string{"B"},
				Frames:        []FrameSample{sampleFrame(1, 4, tc.otherType)},
			}
			if tc.explicit {
				other.AllowedTypes = []string{"I"}
			}
			if tc.manual {
				other.Anchors = []MatchAnchor{{MainSeconds: 1, SourceSeconds: 4}}
			}
			result, err := Match([]SampledSource{main, other}, "main", MatchOptions{MainMoments: []float64{1}})
			if err != nil || len(result.Rows) != 1 || result.Rows[0].Complete != tc.resolved || result.Rows[0].Frames[1].Resolved != tc.resolved {
				t.Fatalf("rows=%+v error=%v", result.Rows, err)
			}
		})
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
	main.Frames[1].TimestampSeconds = 1.4
	result, err = Match([]SampledSource{main, other}, "main", MatchOptions{Count: 2})
	complete := 0
	for _, row := range result.Rows {
		if row.Complete {
			complete++
		}
	}
	if err != nil || len(result.Rows) != 2 || complete != 1 {
		t.Fatalf("nearby main moments reused the same automatic target: %+v %v", result, err)
	}
	main.Frames[1].TimestampSeconds = 20
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

func TestMatchingChecksAmbiguityAfterPictureTypeSubstitution(t *testing.T) {
	frame := func(seconds, score float64, kind string) matchingFrame {
		a, b := score/math.Sqrt2, math.Sqrt(1-score*score)/math.Sqrt(6)
		return matchingFrame{
			frame:     FrameSample{TimestampSeconds: seconds, FrameType: kind},
			signature: []float64{a + b, -a + b, -2 * b},
		}
	}
	for _, defaults := range []bool{false, true} {
		kind := "I"
		if defaults {
			kind = "B"
		}
		matched := matchFrame(frame(1, 1, "I"), matchingColumn{
			source: SampledSource{
				ID:            "encode",
				AllowedTypes:  []string{kind},
				PreferBFrames: defaults,
			},
			frames: []matchingFrame{frame(1, 1, "P"), frame(1.2, .9806, kind), frame(10, .9578, kind)},
		})
		if matched.TimestampSeconds != 1.2 || matched.Resolved || matched.Reason != "ambiguous_visual_match" {
			t.Fatalf("defaults=%t picture-type substitution concealed target ambiguity: %+v", defaults, matched)
		}
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
		Count:       3,
		DarkCount:   1,
		MotionCount: 1,
	})
	if err != nil || len(result.Rows) != 3 || result.AchievedDark < 1 || result.AchievedMotion < 1 {
		t.Fatalf("mix=%+v err=%v", result, err)
	}
	for _, row := range result.Rows {
		if row.MainSeconds == 1 || !row.Complete {
			t.Fatalf("invalid automatic scene: %+v", row)
		}
	}
	result, err = Match([]SampledSource{source, other}, "main", MatchOptions{Count: 3, DarkCount: 3})
	if err != nil || result.AchievedDark != 1 || len(result.Warnings) == 0 {
		t.Fatalf("unmet mix not reported: %+v %v", result, err)
	}
	source.Frames[3].Motion = .8
	other = source
	other.ID = "other"
	result, err = Match([]SampledSource{source, other}, "main", MatchOptions{
		Count:       1,
		DarkCount:   1,
		MotionCount: 1,
	})
	if err != nil || len(result.Rows) != 1 || result.AchievedDark != 1 || result.AchievedMotion != 1 || result.Rows[0].Scene != "dark,motion" {
		t.Fatalf("overlapping scene was not counted truthfully: %+v err=%v", result, err)
	}
	if _, err := Match([]SampledSource{source, other}, "main", MatchOptions{Count: 1, DarkCount: 2}); err == nil {
		t.Fatal("scene target greater than total rows accepted")
	}
	result, err = Match([]SampledSource{source, other}, "main", MatchOptions{
		Count:       3,
		MainMoments: []float64{3},
		DarkCount:   2,
		MotionCount: 2,
	})
	if err != nil || len(result.Rows) != 1 || result.Rows[0].MainSeconds != 3 || result.RequestedDark != 2 || result.RequestedMotion != 2 ||
		result.AchievedDark != 1 || result.AchievedMotion != 1 || len(result.Warnings) == 0 {
		t.Fatalf("manual moment lost configured scene targets or unmet guidance: %+v err=%v", result, err)
	}
	if _, err := Match([]SampledSource{source, other}, "main", MatchOptions{
		Count:       3,
		MainMoments: []float64{3},
		DarkCount:   4,
	}); err == nil {
		t.Fatal("manual moment allowed a scene target beyond the requested row count")
	}
}
