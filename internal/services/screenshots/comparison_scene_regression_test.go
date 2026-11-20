// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestLocalComparisonSceneClassifierRegression exercises the production decode,
// native observation and classifier entrypoint against externally frozen labels.
// Private media paths and annotations never become repository fixtures.
func TestLocalComparisonSceneClassifierRegression(t *testing.T) {
	manifestPath := os.Getenv("UPBRR_COMPARISON_FLAT_MANIFEST")
	if manifestPath == "" {
		t.Skip("set the private frozen scene manifest")
	}
	var manifest struct{ Source, Labels, Output string }
	read := func(path string, target any) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	read(manifestPath, &manifest)
	var labels struct {
		Cases []struct {
			ID         string  `json:"id"`
			Center     float64 `json:"center"`
			Flat, Dark *bool
		}
	}
	read(manifest.Labels, &labels)
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), manifest.Source)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		ID         string
		Flat, Dark bool
		Pictures   int
	}
	outcomes := make([]outcome, 0, len(labels.Cases))
	var counts [2][4]int // TP, FP, FN, TN for flat and dark, with separate denominators.
	for _, label := range labels.Cases {
		frames, err := service.AnalyzeComparisonSource(t.Context(), ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    max(0, label.Center-2),
			DurationSeconds: 4,
		})
		if err != nil || len(frames) == 0 {
			t.Fatalf("case %s: analysis failed: %v", label.ID, err)
		}
		result := outcome{ID: label.ID, Pictures: len(frames)}
		// Labels describe the frozen central moment, not any positive elsewhere
		// in a four-second window. Flat uses its nearest actual sparse anchor.
		flatDistance, darkDistance := math.Inf(1), math.Inf(1)
		for _, frame := range frames {
			distance := math.Abs(frame.RelativeTimestampSeconds - label.Center)
			if distance < darkDistance {
				darkDistance = distance
				result.Dark = comparisons.SceneQualified(frame.Evidence, 1)
			}
			if frame.AnalysisAnchor && distance < flatDistance {
				flatDistance = distance
				result.Flat = distance <= .13 && comparisons.SceneQualified(frame.Evidence, 2)
			}
		}
		for i, expected := range []*bool{label.Flat, label.Dark} {
			if expected == nil {
				continue
			}
			observed := result.Flat
			if i == 1 {
				observed = result.Dark
			}
			switch {
			case observed && *expected:
				counts[i][0]++
			case observed:
				counts[i][1]++
			case *expected:
				counts[i][2]++
			default:
				counts[i][3]++
			}
		}
		outcomes = append(outcomes, result)
		t.Logf("case=%s flat=%t dark=%t pictures=%d", label.ID, result.Flat, result.Dark, result.Pictures)
	}
	if manifest.Output != "" {
		data, err := json.MarshalIndent(struct {
			Counts   [2][4]int
			Outcomes []outcome
		}{counts, outcomes}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest.Output, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tp, fp, fn, tn := counts[0][0], counts[0][1], counts[0][2], counts[0][3]
	precision, recall := float64(tp)/float64(max(1, tp+fp)), float64(tp)/float64(max(1, tp+fn))
	t.Logf("flat real=%d tp=%d fp=%d fn=%d tn=%d precision=%.4f recall=%.4f dark_counts=%v", tp+fp+fn+tn, tp, fp, fn, tn, precision, recall, counts[1])
	if tp+fp+fn+tn != 33 || precision < .85 || recall < .8 {
		t.Fatal("the frozen real-only flat regression gate failed")
	}
}
