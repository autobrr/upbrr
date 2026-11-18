// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonCalibrationRejectsChangedBindings(t *testing.T) {
	b := workflowMediaBuilder{media: &mediaModule{}}
	facts := screenshots.ComparisonSource{
		Width:    1920,
		Height:   1080,
		HDRFacts: api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatSDR}},
	}
	private := workflowMediaPrivateArtifacts{
		ComparisonAnalysisExecutable: "decoder-generation",
		ComparisonSources: map[string]workflowComparisonSource{
			"main":  {Source: facts, Fingerprint: "main-content"},
			"other": {Source: facts, Fingerprint: "selected-programme-content"},
		},
	}
	plan := api.ComparisonPlan{MainSourceID: "main", Sources: []api.ComparisonSource{{ID: "main"}, {ID: "other"}}}
	profile, err := b.comparisonCalibrationProfile(private, plan, "other")
	if err != nil || profile == "" {
		t.Fatal(err)
	}
	saved := &comparisonCalibration{
		Profile:       profile,
		Scale:         comparisons.PlaybackScale{Numerator: 1, Denominator: 1},
		OffsetSeconds: 2,
		Crop:          &screenshots.ComparisonCrop{Width: 1920, Height: 1080},
	}
	source := private.ComparisonSources["other"]
	source.Calibration = saved
	private.ComparisonSources["other"] = source
	cloned := cloneWorkflowMediaPrivateArtifacts(private)
	cloned.ComparisonSources["other"].Calibration.OffsetSeconds++
	cloned.ComparisonSources["other"].Calibration.Crop.X++
	if saved.OffsetSeconds != 2 || saved.Crop.X != 0 {
		t.Fatal("cloning shared mutable calibration facts")
	}
	for _, change := range []string{"main", "programme", "clock", "framing", "decoder", "main normalization", "secondary normalization", "capture settings"} {
		t.Run(change, func(t *testing.T) {
			changed, builder := cloneWorkflowMediaPrivateArtifacts(private), b
			source := changed.ComparisonSources["other"]
			switch change {
			case "main":
				main := changed.ComparisonSources["main"]
				main.Fingerprint += "-replacement"
				changed.ComparisonSources["main"] = main
			case "programme":
				source.Fingerprint += "-replacement"
			case "clock":
				source.Source.StartTimeSeconds++
			case "framing":
				source.Source.WidthScale = 2
			case "decoder":
				changed.ComparisonAnalysisExecutable += "-replacement"
			case "main normalization":
				main := changed.ComparisonSources["main"]
				main.Source.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, DolbyVisionProfile: "5"}
				changed.ComparisonSources["main"] = main
			case "secondary normalization":
				source.Source.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, DolbyVisionProfile: "5"}
			case "capture settings":
				builder.config.ScreenshotHandling.UseLibplacebo = true
				builder.config.ScreenshotHandling.Desat = 42
				builder.config.ScreenshotHandling.TonemapAlgorithm = "hable"
			}
			changed.ComparisonSources["other"] = source
			profile, err := builder.comparisonCalibrationProfile(changed, plan, "other")
			if err != nil {
				t.Fatal(err)
			}
			if _, reused := saved.prediction(profile, source.Source); reused != (change == "capture settings") {
				t.Fatal("calibration compatibility lost its effective analysis policy")
			}
		})
	}
}
