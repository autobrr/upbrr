// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func gradeFixture(a, b [3]float32) (pictureDecodeRequest, []byte) {
	r := decodedProcessFixture()
	r.inputs = append(r.inputs, r.inputs[0])
	r.times = append(r.times, r.times[0])
	r.selected = [][]int{{0}, {2}}
	r.crop = &api.HDRCrop{Width: 2, Height: 2}
	frame := make([]byte, 4*2*12)
	for channel, plane := range [3]int{2, 0, 1} {
		for y := range 2 {
			for x := range 4 {
				value := a[channel]
				if x >= 2 {
					value = b[channel]
				}
				binary.LittleEndian.PutUint32(frame[4*(plane*8+y*4+x):], math.Float32bits(value))
			}
		}
	}
	return r, frame
}

func TestGradeAbsoluteLightAndSignedPQDifference(t *testing.T) {
	request, frame := gradeFixture([3]float32{100, 100, 100}, [3]float32{200, 200, 200})
	plan := &gradeMeasurement{}
	plan.observe(0, request, frame)
	o := plan.observations[0]
	if !o.Complete || math.Abs(o.A.MeanYNits-100) > 1e-10 || o.A.MaxRGBNits != 100 || math.Abs(o.B.MeanYNits-200) > 1e-10 || o.B.MaxRGBNits != 200 {
		t.Fatalf("absolute light: %+v", o)
	}
	// ST 2084 normalized code values for 100 and 200 nits, independently
	// evaluated from the published rational constants, not the production helper.
	want := 0.5791332452435196 - 0.508078421517399
	for channel := range 3 {
		if math.Abs(o.Difference.SignedMeanPQ[channel]-want) > 1e-10 || math.Abs(o.Difference.P95AbsolutePQ[channel]-want) > 1e-10 {
			t.Fatalf("signed PQ delta: %+v", o.Difference)
		}
	}
	request, frame = gradeFixture([3]float32{200, 200, 200}, [3]float32{100, 100, 100})
	plan = &gradeMeasurement{}
	plan.observe(0, request, frame)
	if math.Abs(plan.observations[0].Difference.SignedMeanPQ[0]+want) > 1e-10 {
		t.Fatal("source direction was lost")
	}
}

func TestGradeChromaticityPrimariesAndWhite(t *testing.T) {
	// ITU-R BT.2020-2 Table 2 (CIE 1931 x,y), including D65 reference white.
	for _, fixture := range []struct {
		rgb  [3]float32
		x, y float64
	}{
		{[3]float32{100, 0, 0}, 0.708, 0.292},
		{[3]float32{0, 100, 0}, 0.170, 0.797},
		{[3]float32{0, 0, 100}, 0.131, 0.046},
		{[3]float32{100, 100, 100}, 0.3127, 0.3290},
	} {
		request, frame := gradeFixture(fixture.rgb, fixture.rgb)
		plan := &gradeMeasurement{}
		plan.observe(0, request, frame)
		for _, points := range plan.points {
			if len(points) != 1 || math.Abs(points[0].X-fixture.x) > 1e-10 || math.Abs(points[0].Y-fixture.y) > 1e-10 {
				t.Fatalf("primary %v: %+v", fixture.rgb, points)
			}
		}
		if fixture.rgb[1] == 0 && fixture.rgb[2] == 0 && math.Abs(plan.observations[0].A.MeanYNits-26.27002120112671) > 1e-10 {
			t.Fatal("MaxRGB mislabeled as luminance")
		}
	}
}

func TestGradeInvalidPixelsDoNotAverageIntoValidEvidence(t *testing.T) {
	for _, invalid := range []float32{float32(math.NaN()), float32(math.Inf(1)), -1, 10001} {
		request, frame := gradeFixture([3]float32{100, 100, 100}, [3]float32{100, 100, 100})
		binary.LittleEndian.PutUint32(frame, math.Float32bits(invalid))
		plan := &gradeMeasurement{}
		plan.observe(0, request, frame)
		o := plan.observations[0]
		if o.Complete || o.A.InvalidPixels != 1 || o.A.ReducedInvalid != 1 || o.B.InvalidPixels != 0 || o.Difference.EligiblePixels != 0 {
			t.Fatalf("invalid RGB became evidence: %+v", o)
		}
	}
	request, frame := gradeFixture([3]float32{-0.001, 100, 100}, [3]float32{0, 100, 100})
	plan := &gradeMeasurement{}
	plan.observe(0, request, frame)
	if o := plan.observations[0]; !o.Complete || o.A.RoundoffPixels != 4 || o.A.InvalidPixels != 0 || o.Difference.MaxAbsolutePQ[0] != 0 {
		t.Fatalf("bounded roundoff was not recorded: %+v", o)
	}
	request, frame = gradeFixture([3]float32{}, [3]float32{})
	plan = &gradeMeasurement{}
	plan.observe(0, request, frame)
	if plan.observations[0].Complete || len(plan.points[0]) != 0 {
		t.Fatal("empty luminance eligibility completed comparison")
	}
}

func TestGradeLinearAreaAverageAndBoundedDeterministicSamples(t *testing.T) {
	request, frame := gradeFixture([3]float32{}, [3]float32{25, 25, 25})
	for plane := range 3 {
		binary.LittleEndian.PutUint32(frame[4*plane*8:], math.Float32bits(100))
	}
	plan := &gradeMeasurement{}
	plan.observe(0, request, frame)
	if o := plan.observations[0]; o.A.MaxRGBNits != 25 || math.Abs(o.A.MeanYNits-25) > 1e-10 || o.Difference.MaxAbsolutePQ != [3]float64{} {
		t.Fatalf("linear area average: %+v", o)
	}
	other := &gradeMeasurement{}
	other.observe(0, request, frame)
	if !reflect.DeepEqual(plan.points, other.points) {
		t.Fatal("chromaticity samples are not deterministic")
	}
	for count := 1; count <= 120; count++ {
		total, pairs := 0, 0
		for i := range count {
			budget := gradeColourBudget(i, count)
			total += budget
			if budget > 0 {
				pairs++
			}
		}
		if total != 4096 || pairs != min(12, count) {
			t.Fatalf("colour sample bounds count=%d total=%d pairs=%d", count, total, pairs)
		}
	}
}
