// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
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
