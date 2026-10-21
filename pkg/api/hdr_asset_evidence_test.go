// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestValidationHDRArtifactsRequireExactSelectedHostedAnalysis(t *testing.T) {
	kind := HDRArtifactGradePlot
	width, height := kind.ImageDimensions()
	exact := &ExactMediaAssets{
		HDRAnalysis: &HDRAnalysisRef{ID: "analysis", Revision: 1},
		HDRPlots: []HDRDescriptionPlot{{
			ArtifactID: "grade",
			Kind:       kind,
			TargetID:   HDRTargetID("synthetic", ""),
			Image: ScreenshotImage{
				Path:    "grade.png",
				Purpose: ScreenshotPurposeHDRAnalysis,
				Width:   width,
				Height:  height,
			},
		}},
		HDRUploadHosts: map[string]string{"LST": "test-host"},
		HDRUploads: []UploadedImageLink{{
			ImagePath: "grade.png",
			Host:      "test-host",
			Purpose:   ScreenshotPurposeHDRAnalysis,
			RawURL:    "https://images.example/grade.png",
		}},
	}
	for _, tc := range []struct {
		name            string
		change          func(*ExactMediaAssets)
		artifacts, urls int
	}{
		{"exact", func(*ExactMediaAssets) {}, 1, 1},
		{"no authority", func(a *ExactMediaAssets) { a.HDRAnalysis = nil }, 0, 0},
		{"missing revision", func(a *ExactMediaAssets) { a.HDRAnalysis.Revision = 0 }, 0, 0},
		{"no artifact identity", func(a *ExactMediaAssets) { a.HDRPlots[0].ArtifactID = "" }, 0, 0},
		{"wrong purpose", func(a *ExactMediaAssets) { a.HDRPlots[0].Image.Purpose = ScreenshotPurposeFinal }, 0, 0},
		{"unselected host", func(a *ExactMediaAssets) { a.HDRUploadHosts["LST"] = "other" }, 1, 0},
		{"other tracker", func(a *ExactMediaAssets) { a.HDRUploads[0].UsageScope = "tracker:OTHER" }, 1, 0},
		{"unhosted", func(a *ExactMediaAssets) { a.HDRUploads = nil }, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := exact.Clone()
			tc.change(assets)
			got := validationHDRArtifacts(assets, "LST")
			if len(got) != tc.artifacts {
				t.Fatalf("artifacts=%+v", got)
			}
			if len(got) > 0 && len(got[0].ImageURLs) != tc.urls {
				t.Fatalf("URLs=%+v", got)
			}
		})
	}
}

func TestHDRArtifactEvidenceRetainsOnlyCompatibleFinalAssociations(t *testing.T) {
	kind := HDRArtifactGradePlot
	width, height := kind.ImageDimensions()
	exact := &ExactMediaAssets{
		HDRAnalysis: &HDRAnalysisRef{ID: "analysis", Revision: 7},
		HDRPlots: []HDRDescriptionPlot{{
			ArtifactID: "grade",
			Kind:       kind,
			TargetID:   HDRTargetID("synthetic", ""),
			Image: ScreenshotImage{
				Path:    "grade.png",
				Purpose: ScreenshotPurposeHDRAnalysis,
				Width:   width,
				Height:  height,
			},
		}},
	}
	retained := validationHDRArtifacts(exact, "LST")
	retained[0].ImageURLs = []string{"https://images.example/grade.png"}
	subject := UploadSubject{TrackerHDRArtifacts: map[string][]HDRArtifactEvidence{"LST": retained}, DescriptionGroupsFinal: true}
	for _, mixed := range []bool{false, true} {
		if mixed {
			subject.ExactMedia = exact.Clone()
		}
		got := subject.HDRArtifactEvidence("LST")
		if len(got) != 1 || len(got[0].ImageURLs) != 1 || got[0].Analysis != *exact.HDRAnalysis || got[0].ArtifactID != "grade" {
			t.Fatalf("retained exact evidence lost: %+v", got)
		}
		got[0].ImageURLs[0] = "changed"
		if retained[0].ImageURLs[0] == "changed" {
			t.Fatal("retained URLs were not detached")
		}
	}
	subject.ExactMedia.HDRAnalysis.Revision++
	if got := subject.HDRArtifactEvidence("LST"); len(got) != 1 || len(got[0].ImageURLs) != 0 {
		t.Fatalf("stale final links survived exact analysis revision: %+v", got)
	}
	if got := subject.HDRArtifactEvidence("OTHER"); len(got) != 1 || len(got[0].ImageURLs) != 0 {
		t.Fatalf("retained links crossed trackers: %+v", got)
	}
	subject.ExactMedia = exact.Clone()
	subject.ExactMedia.HDRPlots[0].GenerationComplete = !retained[0].GenerationComplete
	if got := subject.HDRArtifactEvidence("LST"); len(got) != 1 || len(got[0].ImageURLs) != 0 {
		t.Fatalf("retained completeness survived changed exact measurement state: %+v", got)
	}
	subject.ExactMedia.HDRAnalysis = nil
	if got := subject.HDRArtifactEvidence("LST"); len(got) != 0 {
		t.Fatalf("malformed exact HDR channel used retained fallback: %+v", got)
	}
}

func TestDescriptionSetClonePreservesDetachedHDRArtifacts(t *testing.T) {
	original := DescriptionSet{HDRArtifacts: map[TrackerID][]HDRArtifactEvidence{"LST": {{
		Analysis:   HDRAnalysisRef{ID: "analysis", Revision: 2},
		ArtifactID: "grade",
		Kind:       HDRArtifactGradePlot,
		TargetID:   HDRTargetID("synthetic", ""),
		ImageURLs:  []string{"https://images.example/grade.png"},
	}}}}
	cloned, err := original.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if len(cloned.HDRArtifacts["LST"]) != 1 || cloned.HDRArtifacts["LST"][0].ArtifactID != "grade" {
		t.Fatalf("HDR evidence lost through persistence clone: %+v", cloned.HDRArtifacts)
	}
	cloned.HDRArtifacts["LST"][0].ImageURLs[0] = "changed"
	if original.HDRArtifacts["LST"][0].ImageURLs[0] == "changed" {
		t.Fatal("snapshot shares retained URL storage")
	}
}

func TestHDRArtifactGenerationCompleteRequiresCompletedMeasurement(t *testing.T) {
	artifact := HDRTypedArtifact{
		ID:      "grade",
		Kind:    HDRArtifactGradePlot,
		Profile: HDRAnalysisProfileV2,
	}
	for _, test := range []struct {
		name     string
		state    HDRDecodedState
		measured int
		member   bool
		complete bool
	}{
		{"complete measurement", HDRDecodedComplete, 1, true, true},
		{"inconclusive render", HDRDecodedInconclusive, 1, true, false},
		{"no measured samples", HDRDecodedComplete, 0, true, false},
		{"unrelated artifact", HDRDecodedComplete, 1, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := HDRDecodedResult{
				Mode:     HDRDecodedGrade,
				State:    test.state,
				Measured: test.measured,
				Checks:   []HDRCheckResult{{State: HDRCheckFail}},
			}
			if test.member {
				decoded.Artifacts = []PublicResourceID{artifact.ID}
			}
			target := HDRAnalysisTargetResult{Artifacts: []HDRTypedArtifact{artifact}, Decoded: []HDRDecodedResult{decoded}}
			if got := target.ArtifactGenerationComplete(artifact); got != test.complete {
				t.Fatalf("generation complete=%v want %v", got, test.complete)
			}
		})
	}
	native := HDRTypedArtifact{
		ID:     "metadata",
		Kind:   HDRArtifactHDR10PlusPlot,
		Format: HDRNativeHDR10Plus,
	}
	target := HDRAnalysisTargetResult{Artifacts: []HDRTypedArtifact{native}, Formats: []HDRFormatResult{{Format: HDRNativeHDR10Plus, State: HDRExtractionComplete}}}
	if !target.ArtifactGenerationComplete(native) {
		t.Fatal("completed native extraction lost")
	}
	target.Formats[0].State = HDRExtractionFailed
	if target.ArtifactGenerationComplete(native) {
		t.Fatal("failed native extraction counted as complete")
	}
}
