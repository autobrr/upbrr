// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"math"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// Retain search predictions, never frame correspondence authority. Every row
// independently proves its native pictures, including after a restart.
type comparisonCalibration struct {
	Profile       string
	Scale         comparisons.PlaybackScale
	OffsetSeconds float64
	Crop          *screenshots.ComparisonCrop
}

func (c *comparisonCalibration) clone() *comparisonCalibration {
	if c == nil {
		return nil
	}
	cloned := *c
	if c.Crop != nil {
		cloned.Crop = new(*c.Crop)
	}
	return &cloned
}

func (c *comparisonCalibration) prediction(profile string, source screenshots.ComparisonSource) (comparisons.ConstantAlignment, bool) {
	if c == nil || profile == "" || c.Profile != profile || c.Scale.Numerator <= 0 || c.Scale.Denominator <= 0 ||
		c.Scale.Numerator > 1_000_000 || c.Scale.Denominator > 1_000_000 || math.IsNaN(c.OffsetSeconds) || math.IsInf(c.OffsetSeconds, 0) ||
		c.Crop != nil && !c.Crop.ValidFor(source.Width, source.Height) {
		return comparisons.ConstantAlignment{}, false
	}
	scale := float64(c.Scale.Numerator) / float64(c.Scale.Denominator)
	if scale < .5 || scale > 2 {
		return comparisons.ConstantAlignment{}, false
	}
	return comparisons.ConstantAlignment{
		OffsetSeconds:    c.OffsetSeconds,
		ScaleNumerator:   c.Scale.Numerator,
		ScaleDenominator: c.Scale.Denominator,
	}, true
}

func (b workflowMediaBuilder) comparisonCalibrationProfile(retained workflowMediaPrivateArtifacts, plan api.ComparisonPlan, id string) (string, error) {
	main, source := retained.ComparisonSources[plan.MainSourceID], retained.ComparisonSources[id]
	if main.Fingerprint == "" || source.Fingerprint == "" || retained.ComparisonAnalysisExecutable == "" {
		return "", nil
	}
	profile, err := api.CanonicalWorkflowFingerprint(struct {
		Version, SceneProfile, Main, Source, MainFingerprint, SourceFingerprint, Executable string
		MainFacts, SourceFacts                                                              screenshots.ComparisonSource
		MatchAspect                                                                         float64
		Libplacebo                                                                          bool
		Algorithm                                                                           string
		Desat                                                                               float64
	}{
		"native-predictor-v1", comparisons.SceneProfile,
		plan.MainSourceID, id, main.Fingerprint, source.Fingerprint, retained.ComparisonAnalysisExecutable,
		main.Source, source.Source, comparisonMatchAspect(retained, plan),
		b.config.ScreenshotHandling.UseLibplacebo, b.config.ScreenshotHandling.TonemapAlgorithm, b.config.ScreenshotHandling.Desat,
	})
	return string(profile), err
}
