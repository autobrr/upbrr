// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"fmt"
	"github.com/autobrr/upbrr/pkg/api"
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
	source.Frames[3].Evidence = &api.ComparisonSceneEvidence{
		Profile:  SceneProfile,
		Eligible: true,
		Dark:     api.ComparisonSceneCategory{State: "verified"},
	}
	source.Frames[5].Motion = .8
	source.Frames[5].Evidence = &api.ComparisonSceneEvidence{
		Profile:  SceneProfile,
		Eligible: true,
		Motion:   api.ComparisonSceneCategory{State: "verified"},
	}
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
	source.Frames[3].Evidence.Motion.State = "verified"
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

func TestConstantAlignmentRequiresTemporalEvidence(t *testing.T) {
	frames := func(start, end, offset float64) []FrameSample {
		var out []FrameSample
		for i := int(math.Round(start * 10)); i < int(math.Round(end*10)); i++ {
			out = append(out, sampleFrame(uint64(int64(math.Round((float64(i)/10-offset)*10))+10000), float64(i)/10, "B"))
		}
		return out
	}
	reference := frames(30, 34, 0)
	for _, offset := range []float64{0, 13.3, -7.2} {
		fitted, reason := DiscoverAlignment(reference, frames(0, 80, offset))
		if reason != "" || math.Abs(fitted.OffsetSeconds-offset) > 1e-7 {
			t.Fatalf("offset=%g fit=%+v reason=%s", offset, fitted, reason)
		}
		independent, reason := DiscoverAlignment(frames(45, 49, 0), frames(35, 75, offset))
		if reason != "" || !fitted.Consistent(independent) {
			t.Fatalf("independent offset=%g fit=%+v reason=%s", offset, independent, reason)
		}
		changed, reason := DiscoverAlignment(frames(45, 49, 0), frames(35, 75, offset+.1))
		if reason != "" || fitted.Consistent(changed) {
			t.Fatalf("persistent one-frame shift accepted: %+v %+v", fitted, changed)
		}
	}
	// One distinct cut can establish frame-accurate correspondence when every
	// reference frame is checked; a count of three large changes is unnecessary.
	cutReference, cutTarget := frames(30, 34, 0), frames(0, 80, 13.3)
	for i := range cutReference {
		seed := uint64(20000)
		if i >= len(cutReference)/2 {
			seed++
		}
		cutReference[i].Luma = sampleFrame(seed, 0, "B").Luma
		cutTarget[433+i].Luma = cutReference[i].Luma
	}
	if fit, reason := DiscoverAlignment(cutReference, cutTarget); reason != "" || math.Abs(fit.OffsetSeconds-13.3) > 1e-7 {
		t.Fatalf("single informative cut fit=%+v reason=%s", fit, reason)
	}
	for _, kind := range []string{"static", "finite static segment", "constant scenes", "repeated", "variable cadence", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			main, target := slices.Clone(reference), frames(0, 80, 0)
			switch kind {
			case "static":
				for i := range main {
					main[i].Luma = reference[0].Luma
				}
			case "finite static segment":
				for i := range main {
					main[i].Luma = reference[0].Luma
					target[300+i].Luma = main[i].Luma
				}
			case "constant scenes":
				for i := range main {
					main[i].Luma = reference[0].Luma
				}
				for i := range target {
					target[i].Luma = reference[0].Luma
				}
			case "repeated":
				for i := range target {
					if target[i].TimestampSeconds >= 50 && target[i].TimestampSeconds < 54 {
						target[i].Luma = frames(target[i].TimestampSeconds-20, target[i].TimestampSeconds-19.9, 0)[0].Luma
					}
				}
			case "variable cadence":
				main[4].TimestampSeconds += .03
			case "unrelated":
				target = frames(0, 80, 500)
			}
			if fitted, reason := DiscoverAlignment(main, target); reason == "" {
				t.Fatalf("%s established offset %+v", kind, fitted)
			}
		})
	}
}

func TestCalibrationCentersSelectSeparatedInformativeMainContent(t *testing.T) {
	reference := make([]FrameSample, 0, 65)
	for second := range 65 {
		picture := uint64(1)
		for _, transition := range []int{10, 30, 50} {
			if second >= transition {
				picture++
			}
		}
		reference = append(reference, sampleFrame(picture, float64(second), "B"))
	}
	centers, reason := CalibrationCenters(reference)
	if reason != "" || !slices.Equal(centers, []float64{10, 30, 50}) {
		t.Fatalf("separated main regions: %v %s", centers, reason)
	}
	if _, reason := CalibrationCenters(reference[24:37]); reason == "" {
		t.Fatal("one informative region supplied three independent proofs")
	}
	if _, reason := CalibrationCenters(reference[:14]); reason == "" {
		t.Fatal("region without complete context was selected")
	}
}

func TestPlaybackAlignmentVerifiesNativeRatesAcrossSeparatedRegions(t *testing.T) {
	scale := PlaybackScale{960, 1001}
	fitted := make([]ConstantAlignment, 0, 3)
	for _, center := range []float64{600, 2400, 4200} {
		var reference, target []FrameSample
		first := int((center - 2) * 24000 / 1001)
		for index := first; float64(index)*1001/24000 < center+2; index++ {
			frame := sampleFrame(uint64(index+1), float64(index)*1001/24000, "B")
			frame.FrameID = fmt.Sprint("main-", index)
			frame.TimebaseTickSeconds = 1.0 / 90000
			reference = append(reference, frame)
		}
		for index := first - 100; index < first+200; index++ {
			frame := sampleFrame(uint64(index+1), float64(index)/25+3, "B")
			frame.FrameID = fmt.Sprint("source-", index)
			frame.TimebaseTickSeconds = 1.0 / 90000
			target = append(target, frame)
		}
		proposals := NativePlaybackScales(reference, target, PlaybackScales(24000, 1001, 30, 1))
		if !slices.Contains(proposals, scale) {
			t.Fatal("misleading nominal rate excluded the native playback scale")
		}
		alignment, reason := DiscoverPlaybackAlignment(reference, target, scale)
		if reason != "" || math.Abs(alignment.OffsetSeconds-3) > 1e-8 {
			t.Fatalf("center=%g alignment=%+v reason=%s", center, alignment, reason)
		}
		fitted = append(fitted, alignment)
		wrong, reason := DiscoverPlaybackAlignment(reference, target, PlaybackScale{24, 25})
		if reason == "" && wrong.Consistent(alignment) {
			t.Fatal("nominal nearby rate established the exact scale")
		}
	}
	if !fitted[0].Consistent(fitted[1]) || !fitted[0].Consistent(fitted[2]) {
		t.Fatal("exact scale drifted between regions")
	}
	if fit, verified := VerifyPlaybackCalibration(fitted); !verified || math.Abs(fit.OffsetSeconds-3) > 1e-8 {
		t.Fatalf("held-out calibration failed: %+v %t", fit, verified)
	}
	shifted := fitted[2]
	shifted.OffsetSeconds += 1.0 / 25
	if fitted[0].Consistent(shifted) {
		t.Fatal("held-out persistent frame shift was accepted")
	}
	if _, verified := VerifyPlaybackCalibration([]ConstantAlignment{fitted[0], fitted[1], shifted}); verified {
		t.Fatal("held-out region adjusted the fitted offset")
	}
}

func TestPlaybackAlignmentAcceptsLowerContrastTemporalEvidence(t *testing.T) {
	before := sampleFrame(1, 0, "B").Luma
	after := sampleFrame(2, 0, "B").Luma
	reference, target := make([]FrameSample, 0, 100), make([]FrameSample, 0, 100)
	for i := range 100 {
		main := sampleFrame(1, float64(i)*1001/24000, "B")
		main.FrameID = fmt.Sprint("reference-", i)
		other := sampleFrame(1, float64(i)/25+3, "B")
		other.FrameID = fmt.Sprint("target-", i)
		if i >= 50 {
			for j := range before {
				main.Luma[j] = byte(.75*float64(before[j]) + .25*float64(after[j]))
				other.Luma[j] = byte(.8*float64(before[j]) + .2*float64(after[j]))
			}
		}
		reference = append(reference, main)
		target = append(target, other)
	}
	alignment, reason := DiscoverPlaybackAlignment(reference, target, PlaybackScale{960, 1001})
	if reason != "" || math.Abs(alignment.OffsetSeconds-3) > 1e-8 {
		t.Fatalf("lower-contrast transition lost correspondence: %+v %s", alignment, reason)
	}
}

func TestPlaybackAlignmentPreservesDuplicateNativeFrames(t *testing.T) {
	reference, target := make([]FrameSample, 0, 100), make([]FrameSample, 0, 200)
	for i := range 100 {
		frame := sampleFrame(uint64(i+1), float64(i)/25, "B")
		frame.FrameID = fmt.Sprint("reference-", i)
		reference = append(reference, frame)
	}
	for i := range 200 {
		frame := sampleFrame(uint64(i/2+1), float64(i)/50+3, "B")
		frame.FrameID = fmt.Sprint("target-", i)
		target = append(target, frame)
	}
	alignment, reason := DiscoverPlaybackAlignment(reference, target, PlaybackScale{1, 1})
	if reason != "" || !alignment.IntervalProof || math.Abs(alignment.OffsetSeconds-3) > .021 {
		t.Fatalf("alignment=%+v reason=%s", alignment, reason)
	}
	if target[0].FrameID == target[1].FrameID {
		t.Fatal("test lost distinct repeated-picture identities")
	}
}

func TestAlignedMatchingPreservesNativeIntervalsWhenFilteringMainFrames(t *testing.T) {
	main := SampledSource{ID: "main", PreferBFrames: true}
	for i, seconds := range []float64{1, 1.04, 2} {
		frame := sampleFrame(uint64(i+1), seconds, "B")
		frame.FrameID = fmt.Sprint("main-", i)
		if i == 2 {
			frame.Luma = make([]byte, len(frame.Luma))
		}
		main.Frames = append(main.Frames, frame)
	}
	point := main
	point.ID = "point"
	interval := SampledSource{ID: "interval", PreferBFrames: true}
	for i, seconds := range []float64{1.02, 1.5, 2.1} {
		frame := sampleFrame(1, seconds, "B")
		frame.FrameID = fmt.Sprint("interval-", i)
		if i == 0 {
			frame.FrameType = "P"
		}
		interval.Frames = append(interval.Frames, frame)
	}
	result, err := AlignedMatch([]SampledSource{main, point, interval}, "main", MatchOptions{Count: 1}, map[string]ConstantAlignment{
		"point":    {ToleranceSeconds: .001, ReferenceFrameIDs: []string{"main-0", "main-2"}},
		"interval": {ToleranceSeconds: .001, IntervalProof: true},
	}, nil)
	if err != nil || len(result.Rows) != 1 || !result.Rows[0].Complete {
		t.Fatalf("matching native interval failed: %+v err=%v", result, err)
	}
	row := result.Rows[0]
	if row.MainSeconds != 1 || row.Frames[2].FrameID != "interval-0" || row.Frames[2].FrameType != "P" {
		t.Fatalf("a filtered gap authorized a later B picture: %+v", row)
	}
	if main.Frames[0].DisplayEndSeconds != 0 || len(main.Frames) != 3 {
		t.Fatal("matching mutated the caller's native samples")
	}
}

func TestAlignedMatchingKeepsStaticPredictionAndRejectsReuse(t *testing.T) {
	main := SampledSource{
		ID:            "main",
		PreferBFrames: true,
		AllowedTypes:  []string{"B"},
	}
	other := SampledSource{
		ID:            "other",
		PreferBFrames: true,
		AllowedTypes:  []string{"B"},
	}
	for _, seconds := range []float64{1, 4, 8} {
		main.Frames = append(main.Frames, sampleFrame(1, seconds, "B"))
		nearby := sampleFrame(1, seconds+10, "B")
		nearby.Luma = slices.Clone(nearby.Luma)
		nearby.Luma[0]++
		other.Frames = append(other.Frames, nearby, sampleFrame(1, seconds+10.04, "B"))
	}
	offsets := map[string]ConstantAlignment{"other": {OffsetSeconds: 10, ToleranceSeconds: .01}}
	result, err := AlignedMatch([]SampledSource{main, other}, "main", MatchOptions{Count: 3}, offsets, nil)
	if err != nil || len(result.Rows) != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, row := range result.Rows {
		if !row.Complete || math.Abs(row.Frames[1].TimestampSeconds-row.MainSeconds-10) > 1e-7 {
			t.Fatalf("static scene drifted: %+v", row)
		}
	}
	main.Frames = []FrameSample{sampleFrame(1, 1, "B"), sampleFrame(1, 1.1, "B")}
	other.Frames = []FrameSample{sampleFrame(1, 11, "P")}
	result, err = AlignedMatch([]SampledSource{main, other}, "main", MatchOptions{Count: 2}, offsets, nil)
	complete := 0
	for _, row := range result.Rows {
		if row.Complete {
			complete++
		}
	}
	if err != nil || complete != 1 {
		t.Fatalf("target reuse was accepted: %+v err=%v", result, err)
	}
	previous := []MatchedRow{}
	for _, row := range result.Rows {
		if row.Complete {
			previous = append(previous, row)
		}
	}
	continued, err := AlignedMatch([]SampledSource{main, other}, "main", MatchOptions{Count: 1}, offsets, previous)
	if err != nil || len(continued.Rows) != 1 || continued.Rows[0].Complete {
		t.Fatalf("a later bounded region reused a retained target: %+v err=%v", continued, err)
	}
	other.Frames[0].FrameType = "I"
	result, err = AlignedMatch([]SampledSource{main, other}, "main", MatchOptions{Count: 1}, offsets, nil)
	if err != nil || len(result.Rows) != 1 || result.Rows[0].Complete {
		t.Fatalf("non-I main gained I fallback: %+v err=%v", result, err)
	}
}

func TestAlignedMatchingRequiresAnchorAtSelectedMainFrame(t *testing.T) {
	main := SampledSource{ID: "main", Frames: []FrameSample{sampleFrame(1, 1, "B"), sampleFrame(2, 2, "B")}}
	main.Frames[0].FrameID, main.Frames[1].FrameID = "first", "selected"
	other := SampledSource{ID: "other", Frames: []FrameSample{sampleFrame(2, 12, "B")}}
	options := MatchOptions{Count: 1, MainFrameID: "selected"}
	for _, tc := range []struct {
		name           string
		anchor         float64
		aligned, valid bool
	}{
		{"unrelated anchor", 1, false, false},
		{"exact anchor", 2, false, true},
		{"checked alignment with unrelated anchor", 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other.Anchors = []MatchAnchor{{MainSeconds: tc.anchor, SourceSeconds: 12}}
			alignments := map[string]ConstantAlignment{}
			if tc.aligned {
				alignments["other"] = ConstantAlignment{OffsetSeconds: 10, ToleranceSeconds: .01}
			}
			result, err := AlignedMatch([]SampledSource{main, other}, "main", options, alignments, nil)
			if !tc.valid {
				if err == nil && len(result.Rows) > 0 && result.Rows[0].Complete {
					t.Fatal("an unrelated anchor authorized a selected frame")
				}
				return
			}
			if err != nil || len(result.Rows) != 1 || !result.Rows[0].Complete || result.Rows[0].Frames[1].TimestampSeconds != 12 {
				t.Fatalf("checked correspondence was lost: %+v %v", result, err)
			}
		})
	}
}

func TestMatchingPrioritizesRequestedFlatScene(t *testing.T) {
	main := SampledSource{ID: "main", Frames: []FrameSample{sampleFrame(1, 1, "B"), sampleFrame(2, 5, "B")}}
	main.Frames[1].Evidence = &api.ComparisonSceneEvidence{
		Profile:  SceneProfile,
		Eligible: true,
		Flat:     api.ComparisonSceneCategory{State: "verified", Score: .5},
	}
	other := main
	other.ID = "other"
	result, err := Match([]SampledSource{main, other}, "main", MatchOptions{Count: 1, FlatCount: 1})
	if err != nil || len(result.Rows) != 1 || !result.Rows[0].Complete || result.Rows[0].MainSeconds != 5 || result.AchievedFlat != 1 {
		t.Fatalf("available flat evidence was not selected: %+v %v", result, err)
	}
}
