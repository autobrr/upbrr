// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestManualEditionCompoundsAndOpaqueWording(t *testing.T) {
	for _, test := range []struct {
		input, cut, edition, presentation string
		features                          []api.ReleaseFeature
	}{
		{"Director's Cut / Hybrid", "Director's Cut", "", "", []api.ReleaseFeature{api.ReleaseFeatureHybrid}},
		{"Open Matte / 4K Restoration / Hybrid", "", "", "Open Matte", []api.ReleaseFeature{api.ReleaseFeature4KRestoration, api.ReleaseFeatureHybrid}},
		{"Theatrical / Hybrid", "", "", "", []api.ReleaseFeature{api.ReleaseFeatureHybrid}},
		{"2D/3D Edition", "", "", "", []api.ReleaseFeature{api.ReleaseFeature2D3DEdition}},
		{"Custom Hybrid Cut", "", "Custom Hybrid Cut", "", nil},
		{"Custom / Hybrid", "", "Custom / Hybrid", "", nil},
		{"Rifftrax", "", "Rifftrax", "", nil},
	} {
		t.Run(test.input, func(t *testing.T) {
			parts := manualEditionParts(test.input)
			if parts.Cut != test.cut || parts.Edition != test.edition || parts.Presentation != test.presentation || !slices.Equal(parts.Features, test.features) {
				t.Fatalf("parts=%+v", parts)
			}
		})
	}
}

func TestManualEditionPreservesIndependentFacts(t *testing.T) {
	for _, edition := range []string{"", "Custom wording", "Director's Cut / Hybrid"} {
		state := preparationstate.State{
			Presentation:         "Open Matte",
			ReleaseFeatures:      []api.ReleaseFeature{api.ReleaseFeature4KRestoration},
			EditionDisplay:       []api.EditionDisplayPart{{Text: "4K Restoration", Feature: api.ReleaseFeature4KRestoration}},
			ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: new(edition)},
		}
		applyReleaseNameValueOverrides(&state)
		if state.Presentation != "Open Matte" || !slices.Contains(state.ReleaseFeatures, api.ReleaseFeature4KRestoration) || !strings.Contains(api.EditionDisplayText(state.EditionDisplay), "4K Restoration") {
			t.Fatalf("independent fact erased: %+v", state)
		}
	}
}

func TestIndependentManualControlsPrecedenceAndNaming(t *testing.T) {
	for _, edition := range []string{"", "Collector's"} {
		state := preparationstate.State{
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Title:      "Example Movie",
				Year:       2026,
				Resolution: "1080p",
			},
			Type:            "ENCODE",
			Source:          "BluRay",
			VideoEncode:     "x264",
			Presentation:    "IMAX",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureExtras},
			ReleaseNameOverrides: api.ReleaseNameOverrides{
				Edition:         new(edition),
				Presentation:    new("Open Matte"),
				ReleaseFeatures: new([]api.ReleaseFeature{api.ReleaseFeature4KRestoration, api.ReleaseFeatureHybrid}),
			},
		}
		applyReleaseNameValueOverrides(&state)
		RebuildReleaseName(&state, api.NopLogger{})
		expected := strings.TrimSpace(edition + " 4K Restoration Open Matte Hybrid")
		if !strings.Contains(state.ReleaseName, "2026 "+expected+" 1080p") || strings.Contains(state.ReleaseName, "Extras") {
			t.Fatalf("placement=%q", state.ReleaseName)
		}
		state.ReleaseNameOverrides.Edition = new("Director's Cut / Hybrid")
		state.ReleaseNameOverrides.Presentation = new("")
		state.ReleaseNameOverrides.ReleaseFeatures = new([]api.ReleaseFeature{})
		applyReleaseNameValueOverrides(&state)
		RebuildReleaseName(&state, api.NopLogger{})
		if state.Presentation != "" || len(state.ReleaseFeatures) != 0 || strings.Contains(state.ReleaseName, "Hybrid") || strings.Contains(state.ReleaseName, "Restoration") {
			t.Fatalf("None did not win: %+v", state)
		}
	}
}

func TestManualAnnotationsSurviveMultiEditionSet(t *testing.T) {
	state := preparationstate.State{
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Title:      "Example Movie",
			Year:       2026,
			Resolution: "1080p",
		},
		Type:                 "ENCODE",
		Source:               "BluRay",
		EditionSet:           "2in1",
		Cut:                  "Director's Cut",
		Edition:              "Collector's",
		ReleaseNameOverrides: api.ReleaseNameOverrides{Presentation: new("Open Matte"), ReleaseFeatures: new([]api.ReleaseFeature{api.ReleaseFeature4KRestoration, api.ReleaseFeatureHybrid})},
	}
	applyReleaseNameValueOverrides(&state)
	RebuildReleaseName(&state, api.NopLogger{})
	if !strings.Contains(state.ReleaseName, "2026 2in1 4K Restoration Open Matte Hybrid 1080p") || strings.Contains(state.ReleaseName, "Collector's") || strings.Contains(state.ReleaseName, "Director") {
		t.Fatalf("set controls=%q", state.ReleaseName)
	}
}
