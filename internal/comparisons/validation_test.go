// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSceneCountsPreserveLegacyIntentAndExplicitAuthority(t *testing.T) {
	for _, test := range []struct {
		name         string
		preferences  api.ComparisonPreferences
		dark, motion int
		invalid      bool
	}{
		{"legacy percentages round to rows", api.ComparisonPreferences{
			Count:         7,
			DarkPercent:   2,
			MotionPercent: 30,
		}, 1, 3, false},
		{"explicit zero overrides percentages", api.ComparisonPreferences{
			Count:       7,
			DarkPercent: 100,
			DarkCount:   new(0),
			MotionCount: new(2),
		}, 0, 2, false},
		{"overlap allowed", api.ComparisonPreferences{
			Count:       2,
			DarkCount:   new(2),
			MotionCount: new(2),
		}, 2, 2, false},
		{"negative target", api.ComparisonPreferences{Count: 2, DarkCount: new(-1)}, 0, 0, true},
		{"target exceeds total", api.ComparisonPreferences{Count: 2, MotionCount: new(3)}, 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dark, motion, err := SceneCounts(test.preferences, test.preferences.Count)
			if (err != nil) != test.invalid || dark != test.dark || motion != test.motion {
				t.Fatalf("counts=%d/%d err=%v", dark, motion, err)
			}
		})
	}
}

func TestComparisonValidationRejectsAmbiguousAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.ComparisonPlan)
	}{
		{"duplicate source", func(plan *api.ComparisonPlan) { plan.Sources = append(plan.Sources, plan.Sources[0]) }},
		{"duplicate row", func(plan *api.ComparisonPlan) { plan.Rows = append(plan.Rows, plan.Rows[0]) }},
		{"duplicate cell", func(plan *api.ComparisonPlan) { plan.Rows[0].Cells = append(plan.Rows[0].Cells, plan.Rows[0].Cells[0]) }},
		{"invalid picture type", func(plan *api.ComparisonPlan) { plan.Sources[0].AllowedFrameTypes = []string{"I", "invalid"} }},
		{"unknown main column", func(plan *api.ComparisonPlan) { plan.MainSourceID = "4" }},
		{"ambiguous markup label", func(plan *api.ComparisonPlan) { plan.Sources[0].Label = "Source, [other]\n" }},
		{"entity-escaped delimiter", func(plan *api.ComparisonPlan) { plan.Sources[0].Label = "Source&#44;HD" }},
		{"unsupported overlay label", func(plan *api.ComparisonPlan) {
			plan.Sources[0].Label = "Источник"
			plan.Sources[0].Overlay = true
		}},
		{"unbound image", func(plan *api.ComparisonPlan) { plan.Rows[0].Cells[0].ArtifactID = "" }},
		{"unknown size reference", func(plan *api.ComparisonPlan) { plan.SizeReferenceSourceID = "missing" }},
		{"imported size reference", func(plan *api.ComparisonPlan) { plan.SizeReferenceSourceID = plan.Sources[0].ID }},
		{"imported tone mapping", func(plan *api.ComparisonPlan) { plan.Sources[0].ToneMap = new(true) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, _ := Import(comparisonImportImages(t), "2")
			test.change(&plan)
			if problems := Validate(plan); len(problems) == 0 {
				t.Fatal("ambiguous comparison accepted")
			}
		})
	}
	plan, _ := Import(comparisonImportImages(t), "2")
	plan.Sources[0].Label = "Источник — Source"
	if problems := Validate(plan); len(problems) != 0 {
		t.Fatalf("description-only label was restricted by an unused overlay: %+v", problems)
	}
}

func TestReviewFingerprintBindsRetainedContentAndChoices(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.ComparisonPlan)
	}{
		{"image bytes", func(plan *api.ComparisonPlan) { plan.Rows[0].Cells[0].ContentSHA256 = "different" }},
		{"mapping", func(plan *api.ComparisonPlan) { plan.Rows[0].Cells[0].SourceID = "3" }},
		{"column label", func(plan *api.ComparisonPlan) { plan.Sources[0].Label = "New label" }},
		{"column order", func(plan *api.ComparisonPlan) { plan.Sources[0], plan.Sources[1] = plan.Sources[1], plan.Sources[0] }},
		{"exclusion", func(plan *api.ComparisonPlan) { plan.Rows[0].Excluded = true }},
		{"main column", func(plan *api.ComparisonPlan) { plan.MainSourceID = "1" }},
		{"normal screenshot reuse", func(plan *api.ComparisonPlan) { plan.ReuseMainScreenshots = true }},
		{"render size", func(plan *api.ComparisonPlan) { plan.SizeReferenceSourceID = "2" }},
		{"tone mapping", func(plan *api.ComparisonPlan) { plan.Sources[0].ToneMap = new(false) }},
		{"capture choices", func(plan *api.ComparisonPlan) {
			plan.Sources[0].Overlay = true
			plan.Sources[0].AllowedFrameTypes = []string{"I"}
			plan.Preferences.MotionPercent = 20
			plan.Anchors = []api.ComparisonAnchor{{
				SourceID:      "1",
				MainSeconds:   1,
				SourceSeconds: 2,
			}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, _ := Import(comparisonImportImages(t), "2")
			before, err := ReviewFingerprint(plan)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&plan)
			after, err := ReviewFingerprint(plan)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Fatal("review survived a change to retained comparison authority")
			}
		})
	}
	plan, _ := Import(comparisonImportImages(t), "2")
	before, err := ReviewFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Fingerprint = "derived"
	plan.ApprovedFingerprint = "approval"
	plan.Problems = []api.ComparisonProblem{{Message: "diagnostic"}}
	plan.Warnings = []string{"diagnostic"}
	after, err := ReviewFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("derived review fields changed their own content binding")
	}
}
