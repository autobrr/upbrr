// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
)

func sceneFixture(markers []int) (*dovi.Analysis, []pictureTime) {
	a := &dovi.Analysis{Pictures: make([]dovi.Picture, 120), Records: make([]dovi.RPU, 120)}
	times := make([]pictureTime, 120)
	for i := range a.Pictures {
		a.Pictures[i].HasRPU, a.Pictures[i].RPUIndex = true, uint32(i)
		a.Records[i].DM = &dovi.DM{L1: &dovi.L1{MaxPQ: 2000, AvgPQ: 1000}}
		times[i] = pictureTime{
			Value:     int64(i * 1000),
			Timescale: 24000,
			Valid:     true,
		}
	}
	for _, marker := range markers {
		a.Records[marker].DM.SceneRefresh = 1
	}
	return a, times
}

func TestSceneAuthoredOffsetsAndDrift(t *testing.T) {
	cuts := []int{12, 36, 60, 84, 108}
	for _, offset := range []int{-3, -1, 0, 1, 3} {
		markers := make([]int, len(cuts))
		for i, cut := range cuts {
			markers[i] = cut - offset
		}
		a, times := sceneFixture(markers)
		plan := planSceneMeasurement(a, times)
		for _, index := range plan.selected {
			level := 0.2
			for _, cut := range cuts {
				if index >= cut {
					level = 1 - level
				}
			}
			cue := make([]float64, sceneWidth*sceneHeight)
			for i := range cue {
				cue[i] = level
			}
			plan.observe(index, cue)
		}
		observations, thumbnails, complete := plan.finish()
		if !complete || len(observations) != len(cuts) || len(thumbnails) != 2*len(cuts) {
			t.Fatalf("offset %d coverage: complete=%v candidates=%d thumbnails=%d", offset, complete, len(observations), len(thumbnails))
		}
		for _, observation := range observations {
			if observation.Confidence != "dominant_cut" || observation.BestOffset != offset {
				t.Fatalf("authored offset %d: %#v", offset, observation)
			}
		}
	}
	markers := []int{11, 35, 61, 85, 109}
	a, times := sceneFixture(markers)
	plan := planSceneMeasurement(a, times)
	for _, index := range plan.selected {
		cue := make([]float64, sceneWidth*sceneHeight)
		level := 0.2
		for _, cut := range cuts {
			if index >= cut {
				level = 1 - level
			}
		}
		for i := range cue {
			cue[i] = level
		}
		plan.observe(index, cue)
	}
	observations, _, _ := plan.finish()
	for i, want := range []int{1, 1, -1, -1, -1} {
		if observations[i].BestOffset != want {
			t.Fatal("segment drift collapsed into one offset")
		}
	}
}

func TestSceneAmbiguityAndMissingEvidence(t *testing.T) {
	for _, kind := range []string{"static", "fade", "dark", "two-cuts"} {
		t.Run(kind, func(t *testing.T) {
			a, times := sceneFixture([]int{12, 36, 60, 84, 108})
			plan := planSceneMeasurement(a, times)
			for _, index := range plan.selected {
				value := 0.5
				switch kind {
				case "fade":
					value = float64(index) / 240
				case "dark":
					value = 0.01
					if index >= 12 {
						value = 0.07
					}
				case "two-cuts":
					value = 0.2
					for _, cut := range []int{12, 36, 60, 84, 108} {
						if index == cut-1 || index == cut {
							value = 0.8
						}
					}
				}
				cue := make([]float64, sceneWidth*sceneHeight)
				for i := range cue {
					cue[i] = value
				}
				plan.observe(index, cue)
			}
			observations, _, complete := plan.finish()
			if complete {
				t.Fatal("ambiguous content completed scene check")
			}
			for _, observation := range observations {
				if observation.Confidence != "unclear" {
					t.Fatalf("false confident %s: %#v", kind, observation)
				}
			}
		})
	}
	a, times := sceneFixture([]int{60})
	a.Pictures[59].HasRPU = false
	if plan := planSceneMeasurement(a, times); len(plan.candidates) != 0 {
		t.Fatal("inferred scene transition across a metadata gap")
	}
	a, times = sceneFixture([]int{60})
	a.Records[60].DM.SceneRefresh = 2
	if plan := planSceneMeasurement(a, times); len(plan.candidates) != 0 {
		t.Fatal("non-one refresh flag treated as scene marker")
	}
}

func TestNativeBorderMeasurementAndUncertainty(t *testing.T) {
	const width, height = 32, 24
	frame := make([]byte, width*height*2)
	for y := 4; y < height-4; y++ {
		for x := 2; x < width-2; x++ {
			binary.LittleEndian.PutUint16(frame[2*(y*width+x):], 40000)
		}
	}
	for _, length := range []int{2, 3} {
		a := &dovi.Analysis{Pictures: make([]dovi.Picture, length), Records: []dovi.RPU{{DM: &dovi.DM{L5: &dovi.L5{
			Left:   2,
			Right:  2,
			Top:    3,
			Bottom: 3,
		}}}}}
		for i := range a.Pictures {
			a.Pictures[i].HasRPU = true
		}
		plan, err := planAreaMeasurement(a)
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range plan.selected {
			plan.observe(index, frame, width, height)
		}
		observations, _, complete := plan.finish()
		if complete != (length == 3) || observations[0].Observed != [4]int{2, 2, 4, 4} || observations[0].Delta != [4]int{0, 0, 1, 1} {
			t.Fatalf("border evidence length=%d: %#v complete=%v", length, observations, complete)
		}
	}
	if _, unambiguous := inferBorders(make([]byte, len(frame)), width, height); unambiguous {
		t.Fatal("dark content established a border")
	}
}

func TestSceneAreaReductionPreservesConstantAndEnergy(t *testing.T) {
	frame := make([]byte, 2*2*2)
	for i, value := range []uint16{0, 65535, 65535, 0} {
		binary.LittleEndian.PutUint16(frame[i*2:], value)
	}
	cue := reduceSceneLuma(frame, 2, 2)
	var total float64
	for _, value := range cue {
		total += value
	}
	if total != float64(len(cue))/2 {
		t.Fatalf("area energy %f", total)
	}
}
