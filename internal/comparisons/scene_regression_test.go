// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"encoding/json"
	"os"
	"testing"
)

// TestLocalComparisonMotionRegression runs the adopted detector on the private,
// frozen corpus. Paths and labels stay outside the repository. Real scored cases
// alone enter precision/recall; every negative synthetic control must also pass.
func TestLocalComparisonMotionRegression(t *testing.T) {
	manifestPath := os.Getenv("UPBRR_COMPARISON_MOTION_MANIFEST")
	if manifestPath == "" {
		t.Skip("set the private frozen motion manifest")
	}
	var manifest struct {
		Cases []struct {
			ID, Raw, Observations  string
			Expected, Real, Scored bool
		}
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	tp, fp, fn, tn := 0, 0, 0, 0
	for _, c := range manifest.Cases {
		var input struct {
			Native  []struct{ Time float64 }
			Metrics []struct{ Values map[string]float64 }
		}
		meta, err := os.ReadFile(c.Observations)
		if err != nil {
			t.Fatalf("case %s: observation read failed", c.ID)
		}
		if err := json.Unmarshal(meta, &input); err != nil {
			t.Fatalf("case %s: invalid observations", c.ID)
		}
		data, err := os.ReadFile(c.Raw)
		if err != nil {
			t.Fatalf("case %s: raster read failed", c.ID)
		}
		const width, height = 256, 144
		if len(input.Native) < 2 || len(input.Native) > 50000 || len(data) != len(input.Native)*width*height || len(input.Metrics) != len(input.Native) {
			t.Fatalf("case %s: inconsistent observation/raster records", c.ID)
		}
		points := make([]SceneMeasurement, len(input.Native))
		for i := range points {
			points[i] = SceneMeasurement{Time: input.Native[i].Time, CutScore: input.Metrics[i].Values["lavfi.scd.score"]}
			if i > 0 {
				a, b := data[(i-1)*width*height:i*width*height], data[i*width*height:(i+1)*width*height]
				dt := points[i].Time - points[i-1].Time
				if dt <= 0 {
					t.Fatalf("case %s: invalid native cadence", c.ID)
				}
				points[i].Speed, points[i].Moving, points[i].Reliable, points[i].Text = MeasureMotion(a, b, width, height, dt)
			}
		}
		observed := false
		// This is the frozen motion-only gate, separate from integrated spatial
		// eligibility. The production temporal classifier is exercised directly.
		for _, e := range ClassifyScenes(points, "regression-motion-only") {
			observed = observed || e.Motion.State == "verified"
		}
		if c.Real && c.Scored {
			switch {
			case observed && c.Expected:
				tp++
			case observed:
				fp++
			case c.Expected:
				fn++
			default:
				tn++
			}
		} else if !c.Real && observed != c.Expected {
			t.Errorf("control %s: motion=%t expected=%t", c.ID, observed, c.Expected)
		}
		t.Logf("case=%s real=%t scored=%t motion=%t expected=%t", c.ID, c.Real, c.Scored, observed, c.Expected)
	}
	precision, recall := 0.0, 0.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	t.Logf("real cases=%d tp=%d fp=%d fn=%d tn=%d precision=%.4f recall=%.4f", tp+fp+fn+tn, tp, fp, fn, tn, precision, recall)
	if tp+fp+fn+tn != 12 || precision < .85 || recall < .80 {
		t.Fatal("the frozen real-only V9 regression gate failed")
	}
}
