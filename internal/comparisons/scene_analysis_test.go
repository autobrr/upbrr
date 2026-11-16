// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSceneSparseSupportAndFlatArea(t *testing.T) {
	for _, rate := range []float64{24000.0 / 1001, 24, 25, 50} {
		for _, flatTiles := range []int{7, 8} {
			t.Run(fmt.Sprintf("cadence_%g_tiles_%d", rate, flatTiles), func(t *testing.T) {
				var points []SceneMeasurement
				last := -1.0
				for i := 0; float64(i)/rate < 4; i++ {
					time := float64(i) / rate
					p := SceneMeasurement{Time: time, Tick: 1.0 / 90000}
					if last < 0 || time-last >= .25-1e-12 {
						last = time
						p.Sparse = &SceneSpatialMeasurement{
							Width:  512,
							Height: 288,
							Active: api.ComparisonSceneRegion{
								Width:  512,
								Height: 288,
								Area:   512 * 288,
							},
							CropCertain:       true,
							Mean:              32,
							DarkFraction:      .9,
							NearBlackFraction: .05,
							OriginalContrast:  20,
							BlurredContrast:   16,
						}
						for tile := range 144 {
							p.Sparse.Tiles = append(p.Sparse.Tiles, SceneTile{Region: api.ComparisonSceneRegion{
								X:      tile % 16 * 32,
								Y:      tile / 16 * 32,
								Width:  32,
								Height: 32,
								Area:   1024,
							}, Qualified: tile < flatTiles})
						}
					}
					points = append(points, p)
				}
				classified := ClassifyScenes(points, "sdr")
				dark, flat := false, false
				for _, e := range classified {
					dark = dark || SceneQualified(e, 1)
					flat = flat || SceneQualified(e, 2)
					if e.FlatArea != 0 && e.FlatArea != flatTiles*1024 {
						t.Fatalf("incorrect regional area %d", e.FlatArea)
					}
				}
				if !dark || flat != (flatTiles == 8) {
					t.Fatalf("dark=%t flat=%t at cadence %g", dark, flat, rate)
				}
				// A cut breaks support even when all spatial measurements qualify.
				for i := range points {
					if points[i].Time > .75 && points[i].Time < 1.25 {
						points[i].CutScore = 20
					}
				}
				for i, e := range ClassifyScenes(points, "sdr") {
					if points[i].Time >= .6 && points[i].Time <= 1.4 && (SceneQualified(e, 1) || SceneQualified(e, 2)) {
						t.Fatal("transition supplied sparse support")
					}
				}
			})
		}
	}
}

func TestSceneInventoryRankingAndBestEffort(t *testing.T) {
	frame := func(id string, t, score float64, state string) FrameSample {
		return FrameSample{
			FrameID:          id,
			TimestampSeconds: t,
			Evidence: &api.ComparisonSceneEvidence{
				Profile:  SceneProfile,
				Eligible: true,
				Flat:     api.ComparisonSceneCategory{State: state, Score: score},
			},
		}
	}
	frames := []FrameSample{frame("early", 10, .06, "verified"), frame("late", 90, .2, "verified"), frame("alternative", 50, .04, "rejected")}
	used := map[string]bool{}
	chosen, _, ok := SelectScene(slices.Values(frames), used, nil, [3]int{0, 1, 1}, [3]int{}, [3]int{1, 1, 1}, 1)
	if !ok || chosen.FrameID != "late" {
		t.Fatalf("a missing dark category stalled the later best flat candidate: %+v", chosen)
	}
	used[chosen.FrameID] = true
	chosen, _, ok = SelectScene(slices.Values(frames), used, nil, [3]int{0, 0, 1}, [3]int{}, [3]int{1, 1, 1}, 1)
	if !ok || chosen.FrameID != "early" {
		t.Fatal("failed best match hid the next qualified candidate")
	}
	used[chosen.FrameID] = true
	chosen, _, ok = SelectScene(slices.Values(frames), used, nil, [3]int{0, 0, 1}, [3]int{}, [3]int{1, 1, 1}, 1)
	if !ok || chosen.FrameID != "alternative" || SceneQualified(chosen.Evidence, 2) {
		t.Fatal("best effort invented a category or lost valid content")
	}
	frames = []FrameSample{frame("static-start", 0, 0, "rejected"), frame("static-next", 1, 0, "rejected"), frame("before-change", 20, 0, "rejected"), frame("observed-change", 21, 0, "rejected"), frame("static-end", 130, 0, "rejected")}
	for i := range frames {
		seed := uint64(40)
		if i >= 3 {
			seed = 90
		}
		frames[i].Luma = sampleFrame(seed, 0, "B").Luma
	}
	chosen, _, ok = SelectScene(slices.Values(frames), nil, []MatchedRow{{MainSeconds: 50}, {MainSeconds: 100}}, [3]int{}, [3]int{}, [3]int{}, 1)
	if !ok || chosen.FrameID != "observed-change" {
		t.Fatal("general selection spent native-matching work on static pictures before an informative main section")
	}
}

func TestSelectionRangeProtection(t *testing.T) {
	preferences := api.ComparisonPreferences{Count: 3, SelectionWindow: &api.ComparisonSelectionWindow{Excluded: []api.ComparisonTimeInterval{{Start: 20, End: 30}, {Start: 25, End: 40}}}}
	window, excluded, err := SelectionRange(preferences, 100, false)
	if err != nil || window.Start != 5 || window.End != 90 || len(excluded) != 1 || excluded[0].End != 40 {
		t.Fatalf("incorrect effective range: %+v %+v %v", window, excluded, err)
	}
	for _, time := range []float64{0, 4.99, 20, 35, 90.1} {
		if EligibleTime(time, window, excluded) {
			t.Fatalf("protected moment %g admitted", time)
		}
	}
	if !EligibleTime(50, window, excluded) {
		t.Fatal("narrative interior rejected")
	}
	preferences.ScanMode = "unknown"
	if _, _, err := SelectionRange(preferences, 100, false); err == nil {
		t.Fatal("unknown mode admitted")
	}
}

func TestAlignedQuietMomentRequiresCompleteNativeContext(t *testing.T) {
	reference, target := make([]FrameSample, 0, 40), make([]FrameSample, 0, 40)
	for i := range 40 {
		seed := uint64(5)
		if i >= 10 {
			seed = 9
		}
		frame := sampleFrame(seed, float64(i)/10, "B")
		frame.FrameID, frame.NativeSequence, frame.Occurrence = strconv.Itoa(i), true, "main"
		reference = append(reference, frame)
		frame.TimestampSeconds += 3
		frame.FrameID, frame.Occurrence = "target-"+frame.FrameID, "target"
		target = append(target, frame)
	}
	alignment, reason := DiscoverPlaybackAlignment(reference, target, PlaybackScale{1, 1})
	if reason != "" || !VerifyAlignedMomentContext(reference, target, "25", alignment) {
		t.Fatalf("native quiet context rejected: %s", reason)
	}
	for _, kind := range []string{"coarse", "missing", "join", "different", "static", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			a, b, fit := slices.Clone(reference), slices.Clone(target), alignment
			switch kind {
			case "coarse":
				b[25].NativeSequence = false
			case "missing":
				b = slices.Delete(b, 25, 26)
			case "join":
				b[25].Occurrence = "another occurrence"
			case "different":
				b[25].Luma = sampleFrame(200, 0, "B").Luma
			case "static":
				for i := range a {
					a[i].Luma = a[0].Luma
					b[i].Luma = a[0].Luma
				}
			case "invalid":
				fit.OffsetSeconds = math.NaN()
			}
			if VerifyAlignedMomentContext(a, b, "25", fit) {
				t.Fatal("unproved native context authorized an interior picture")
			}
		})
	}
}

func TestCalibrationCoalescesOnlyEquivalentSearchPredictions(t *testing.T) {
	base := ConstantAlignment{
		ScaleNumerator:   1,
		ScaleDenominator: 1,
		OffsetSeconds:    -13.259,
		ToleranceSeconds: .01125,
	}
	near := ConstantAlignment{
		ScaleNumerator:   636000,
		ScaleDenominator: 635999,
		OffsetSeconds:    -13.26337,
		ToleranceSeconds: .01125,
	}
	chosen, ok := SelectCalibrationHypothesis([]ConstantAlignment{near, base}, 5542)
	if !ok || chosen.Scale() != 1 {
		t.Fatal("rounded nominal rates hid a verified native-cadence predictor")
	}
	near.OffsetSeconds += .1
	if _, ok := SelectCalibrationHypothesis([]ConstantAlignment{base, near}, 5542); ok {
		t.Fatal("distinct calibrated windows were collapsed")
	}
	near = base
	near.ScaleNumerator, near.ScaleDenominator = 1001, 1000
	near.OffsetSeconds -= 2.771
	if _, ok := SelectCalibrationHypothesis([]ConstantAlignment{base, near}, 5542); ok {
		t.Fatal("a mid-film agreement authorized divergent end predictions")
	}
}
