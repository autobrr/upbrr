// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestThreeDCorrectionUsesEffectiveFact(t *testing.T) {
	for _, test := range []struct {
		name, detected, want string
		correction           *bool
	}{
		{"auto detected", "3D", "3D", nil},
		{"auto absent", "", "", nil},
		{"set absent", "", "3D", new(true)},
		{"set subtype", "HSBS", "HSBS", new(true)},
		{"clear", "3D", "", new(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{
				Is3D:                 test.detected,
				Edition:              "Director's Cut",
				Repack:               "REPACK",
				ReleaseNameOverrides: api.ReleaseNameOverrides{ThreeD: test.correction},
			}
			applyReleaseNameValueOverrides(&meta)
			if meta.Is3D != test.want || meta.Edition != "Director's Cut" || meta.Repack != "REPACK" {
				t.Fatalf("effective facts = %+v", meta)
			}
		})
	}
}

func TestThreeDClearRetainsSavedSubtypeIntent(t *testing.T) {
	features := []api.ReleaseFeature{api.ReleaseFeature3DFullSBS, api.ReleaseFeatureHybrid}
	display := []api.EditionDisplayPart{{Text: "Extended"}, {Text: "3D Full SBS", Feature: api.ReleaseFeature3DFullSBS}}
	meta := preparationstate.State{
		Is3D:                 "3D",
		Edition:              "Extended",
		ReleaseFeatures:      features,
		EditionDisplay:       display,
		ReleaseNameOverrides: api.ReleaseNameOverrides{ThreeD: new(false), ReleaseFeatures: &features},
	}
	applyReleaseNameValueOverrides(&meta)
	if meta.Is3D != "" || api.ThreeDName(meta.Is3D, meta.ReleaseFeatures) != "" {
		t.Fatal("Clear left effective 3D")
	}
	if len(features) != 2 || features[0] != api.ReleaseFeature3DFullSBS || len(display) != 2 {
		t.Fatal("Clear mutated retained evidence/intent")
	}
	if len(meta.ReleaseFeatures) != 1 || meta.ReleaseFeatures[0] != api.ReleaseFeatureHybrid || meta.Edition != "Extended" {
		t.Fatal("Clear removed unrelated facts")
	}
}

func TestThreeDClearRemovesOrphanedDisplaySeparators(t *testing.T) {
	display := []api.EditionDisplayPart{{Text: "3D Full SBS", Feature: api.ReleaseFeature3DFullSBS}, {Text: "/"}, {Text: "Rifftrax"}}
	meta := preparationstate.State{
		Is3D:                 "3D",
		EditionDisplay:       display,
		ReleaseNameOverrides: api.ReleaseNameOverrides{ThreeD: new(false)},
	}
	applyReleaseNameValueOverrides(&meta)
	if got := api.EditionDisplayText(meta.EditionDisplay); got != "Rifftrax" {
		t.Fatalf("effective display = %q", got)
	}
	if got := api.EditionDisplayText(display); got != "3D Full SBS / Rifftrax" {
		t.Fatalf("retained evidence changed: %q", got)
	}
}

func TestThreeDSetPreservesDetectedSubtypeWithNoEdition(t *testing.T) {
	for _, clearFeatures := range []bool{false, true} {
		features := []api.ReleaseFeature{api.ReleaseFeature3DFullSBS, api.ReleaseFeatureHybrid}
		display := []api.EditionDisplayPart{{Text: "Extended"}, {Text: "3D Full SBS", Feature: api.ReleaseFeature3DFullSBS}}
		meta := preparationstate.State{
			Is3D:                 "3D",
			Edition:              "Extended",
			Cut:                  "Director's Cut",
			ReleaseFeatures:      features,
			EditionDisplay:       display,
			ReleaseNameOverrides: api.ReleaseNameOverrides{NoEdition: new(true), ThreeD: new(true)},
		}
		if clearFeatures {
			empty := []api.ReleaseFeature{}
			meta.ReleaseNameOverrides.ReleaseFeatures = &empty
		}
		applyReleaseNameValueOverrides(&meta)
		want := "3D"
		if got := api.ThreeDName(meta.Is3D, meta.ReleaseFeatures); got != want {
			t.Fatalf("clear features %t: name = %q, want %q", clearFeatures, got, want)
		}
		wantDisplay := "3D Full SBS"
		if clearFeatures {
			wantDisplay = ""
		}
		if got := api.EditionDisplayText(meta.EditionDisplay); got != wantDisplay {
			t.Fatalf("clear features %t: display = %q, want %q", clearFeatures, got, wantDisplay)
		}
		if meta.Edition != "" || meta.Cut != "" {
			t.Fatal("3D restored suppressed edition identity")
		}
		for _, feature := range meta.ReleaseFeatures {
			if !api.IsThreeDSubtype(feature) {
				t.Fatal("3D restored an unrelated suppressed feature")
			}
		}
		if len(features) != 2 || features[0] != api.ReleaseFeature3DFullSBS || api.EditionDisplayText(display) != "Extended 3D Full SBS" {
			t.Fatal("3D Set mutated saved evidence")
		}
	}
}

func TestThreeDAutoPreservesSubtypeIndependentlyOfMeasuredFact(t *testing.T) {
	for _, test := range []struct {
		name                             string
		manual, noEdition, clearFeatures bool
		correction                       *bool
		wantFact, wantName               string
	}{
		{"detected Auto", false, false, false, nil, "", "3D"},
		{"manual Auto", true, false, false, nil, "", "3D"},
		{"detected Auto without edition", false, true, false, nil, "", "3D"},
		{"manual Auto without edition", true, true, false, nil, "", "3D"},
		{"detected No", false, true, false, new(false), "", ""},
		{"manual No", true, false, false, new(false), "", ""},
		{"explicit features None", false, true, true, nil, "", ""},
		{"explicit Yes", false, true, false, new(true), "3D", "3D"},
	} {
		t.Run(test.name, func(t *testing.T) {
			features := []api.ReleaseFeature{api.ReleaseFeature3DFullSBS}
			display := []api.EditionDisplayPart{{Text: "3D Full SBS", Feature: api.ReleaseFeature3DFullSBS}}
			meta := preparationstate.State{
				Edition:         "Extended",
				ReleaseFeatures: features,
				EditionDisplay:  display,
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					ThreeD:    test.correction,
					NoEdition: &test.noEdition,
				},
			}
			if test.manual {
				meta.ReleaseFeatures = nil
				meta.EditionDisplay = nil
				meta.ReleaseNameOverrides.ReleaseFeatures = &features
			}
			if test.clearFeatures {
				empty := []api.ReleaseFeature{}
				meta.ReleaseNameOverrides.ReleaseFeatures = &empty
			}
			applyReleaseNameValueOverrides(&meta)
			if meta.Is3D != test.wantFact || api.ThreeDName(meta.Is3D, meta.ReleaseFeatures) != test.wantName {
				t.Fatalf("effective measured fact = %q, annotation = %q; want %q, %q", meta.Is3D, api.ThreeDName(meta.Is3D, meta.ReleaseFeatures), test.wantFact, test.wantName)
			}
			wantSubtype := test.wantName != ""
			if got := slices.Contains(meta.ReleaseFeatures, api.ReleaseFeature3DFullSBS); got != wantSubtype {
				t.Fatalf("effective subtype retained = %t, want %t", got, wantSubtype)
			}
			if test.noEdition && meta.Edition != "" {
				t.Fatal("subtype restored suppressed edition")
			}
			if len(features) != 1 || features[0] != api.ReleaseFeature3DFullSBS || api.EditionDisplayText(display) != "3D Full SBS" {
				t.Fatal("correction mutated saved subtype evidence")
			}
		})
	}
}
