// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBoundaryResumeKeepsOnlyUnmeasuredRoles(t *testing.T) {
	for _, test := range []struct {
		boundary, area bool
		windows        [][2]int
	}{
		{true, false, [][2]int{{4, 14}, {17, 24}}},
		{false, true, [][2]int{{0, 0}, {16, 16}}},
		{true, true, [][2]int{{0, 0}, {4, 14}, {16, 16}, {17, 24}}},
	} {
		plan := &BoundaryPlan{areaSelected: []int{0, 16}, report: BoundaryReport{Formats: []BoundaryFormat{{
			Format:            api.HDRNativeDolbyVision,
			NumericCandidates: []int{8, 11},
			VisualCandidates:  []BoundaryVisualCandidate{{Candidate: 21}},
		}}}}
		if err := plan.SelectModes(test.boundary, test.area); err != nil {
			t.Fatal(err)
		}
		windows := make([][2]int, 0, len(plan.report.Windows))
		for _, window := range plan.report.Windows {
			windows = append(windows, [2]int{window.First, window.Last})
		}
		if !slices.Equal(windows, test.windows) {
			t.Fatalf("boundary=%v area=%v windows=%v want=%v", test.boundary, test.area, windows, test.windows)
		}
		if !test.boundary && len(plan.report.Formats) != 0 || !test.area && len(plan.areaSelected) != 0 {
			t.Fatal("retained role still scheduled")
		}
	}
}

func TestBoundaryFormatSelectionAndProfileExclusion(t *testing.T) {
	_, times := sceneFixture([]int{12, 36})
	input := decodedProcessFixture().inputs[0]
	input.Native = &NativeEvidence{DolbyVision: decodedMetadataFixture(t, times), Results: []api.HDRFormatResult{
		{
			Format:     api.HDRNativeHDR10Plus,
			Advertised: true,
			State:      api.HDRExtractionAbsent,
		},
		{
			Format:     api.HDRNativeDolbyVision,
			Advertised: true,
			State:      api.HDRExtractionComplete,
		},
	}}
	if err := input.Native.SelectFormats(api.HDRAnalysisFormatsDolbyVision); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanBoundary(input, false)
	if err != nil || plan.unsupportedFormats != 0 || len(plan.report.Formats) != 1 {
		t.Fatalf("unrequested advertised format reduced coverage: %+v %v", plan, err)
	}
	for _, profile := range []string{"5", "5.0", "5.06"} {
		input.Source.DolbyVisionProfile = profile
		if _, err := PlanBoundary(input, false); err == nil {
			t.Fatalf("source profile %s admitted using contradictory selected metadata", profile)
		}
	}
}
