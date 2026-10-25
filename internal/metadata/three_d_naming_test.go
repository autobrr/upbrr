// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestThreeDAutomaticNamingPlacement(t *testing.T) {
	for _, test := range []struct{ name, kind, disc, country, source string }{
		{"full disc country", "DISC", "BDMV", "CEE", "Blu-ray"},
		{"full disc no country", "DISC", "BDMV", "", "Blu-ray"},
		{"remux", "REMUX", "", "", "BluRay"},
		{"encode", "ENCODE", "", "", "BluRay"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        test.kind,
				DiscType:    test.disc,
				Region:      test.country,
				Source:      test.source,
				Title:       "Example Movie",
				Year:        2026,
				Resolution:  "1080p",
				ThreeD:      "3D Half OU",
				VideoCodec:  "AVC",
				VideoEncode: "x264",
				Audio:       "DTS-HD MA 5.1",
				Tag:         "GRP",
			}
			result := BuildReleaseName(req, api.NopLogger{})
			if result.GeneratedName == nil {
				t.Fatal("missing generated name")
			}
			components := result.GeneratedName.Components
			threeD := releaseNameComponentSliceIndex(components, api.NameRoleThreeD)
			source := releaseNameComponentSliceIndex(components, api.NameRoleSource)
			resolution := releaseNameComponentSliceIndex(components, api.NameRoleResolution)
			if threeD <= resolution || source != threeD+1 || strings.Count(result.Name, "3D") != 1 || components[threeD].AvailableValue != "3D" {
				t.Fatalf("placement = %q", result.Name)
			}
			if test.country != "" && !strings.Contains(result.Name, "1080p CEE 3D Blu-ray") {
				t.Fatalf("country placement = %q", result.Name)
			}
		})
	}
}

func TestThreeDSubtypeNamingPreservesFactsAndEdition(t *testing.T) {
	meta := preparationstate.State{
		Is3D:            "3D",
		Edition:         "Director's Cut",
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DFullSBS},
		EditionDisplay:  []api.EditionDisplayPart{{Text: "Director's Cut"}, {Text: "3D Full SBS", Feature: api.ReleaseFeature3DFullSBS}},
	}
	request := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if request.ThreeD != "3D" || request.EditionDisplay != "Director's Cut" {
		t.Fatalf("naming = %+v", request)
	}
	if meta.Is3D != "3D" || len(meta.ReleaseFeatures) != 1 || len(meta.EditionDisplay) != 2 {
		t.Fatal("naming changed canonical evidence")
	}
}

func TestThreeDWebNamingPlacementWithoutSourceRole(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		for _, kind := range []string{"WEBDL", "WEBRIP"} {
			for _, source := range []string{"", "WEB"} {
				req := api.ReleaseNameRequest{
					Category:    category,
					Type:        kind,
					Source:      source,
					Title:       "Example",
					Year:        2026,
					Resolution:  "1080p",
					ThreeD:      "3D",
					Service:     "NF",
					VideoEncode: "x264",
					Audio:       "DD 5.1",
				}
				result := BuildReleaseName(req, api.NopLogger{})
				if result.GeneratedName == nil {
					t.Fatal("missing document")
				}
				parts := result.GeneratedName.Components
				threeD := releaseNameComponentSliceIndex(parts, api.NameRoleThreeD)
				format := releaseNameComponentSliceIndex(parts, api.NameRoleVideoFormat)
				if threeD < 0 || format != threeD+1 || !strings.Contains(result.Name, "NF 3D WEB") {
					t.Fatalf("%s %s %q: %q", category, kind, source, result.Name)
				}
			}
		}
	}
}

func TestThreeDSourceAliasNaming(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		for _, marker := range []string{"", "3D"} {
			t.Run(category+"/"+marker, func(t *testing.T) {
				req := api.ReleaseNameRequest{
					Category:   category,
					Type:       "DISC",
					DiscType:   "BDMV",
					Title:      "Example Movie",
					Source:     "Blu-ray 3D",
					Resolution: "1080p",
					Region:     "CEE",
					ThreeD:     marker,
				}
				result := BuildReleaseName(req, api.NopLogger{})
				want := "1080p CEE Blu-ray 3D"
				if marker != "" {
					want = "1080p CEE 3D Blu-ray"
				}
				if !strings.Contains(result.Name, want) || strings.Count(result.Name, "3D") != 1 {
					t.Fatalf("name = %q, want %q once", result.Name, want)
				}
				if req.Source != "Blu-ray 3D" || req.ThreeD != marker {
					t.Fatal("naming changed request evidence")
				}
			})
		}
	}
}

func TestThreeDSourceAliasCorrectionPreservesEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		detected string
		control  *bool
		count    int
		source   string
	}{
		{"source-only Auto", "", nil, 1, "Blu-ray 3D"},
		{"detected Auto", "3D", nil, 1, "Blu-ray"},
		{"Yes", "", new(true), 1, "Blu-ray"},
		{"No", "3D", new(false), 0, "Blu-ray"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{
				Type:     "DISC",
				DiscType: "BDMV",
				Source:   "Blu-ray 3D",
				Is3D:     test.detected,
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Example Movie",
					Year:       2026,
					Resolution: "1080p",
					Source:     "Blu-ray 3D",
				},
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					Source: new("Blu-ray 3D"),
					ThreeD: test.control,
				},
			}
			applyReleaseNameValueOverrides(&meta)
			measured := meta.Is3D
			captureAvailableGeneratedName(&meta, api.NopLogger{})
			RebuildReleaseName(&meta, api.NopLogger{})
			if strings.Count(meta.ReleaseName, "3D") != test.count {
				t.Fatalf("name = %q, want %d markers", meta.ReleaseName, test.count)
			}
			source, ok := meta.GeneratedName.Component(api.NameRoleSource)
			if !ok || !source.Manual || source.Value != test.source || source.AvailableValue != test.source {
				t.Fatalf("source component = %+v", source)
			}
			if meta.Source != "Blu-ray 3D" || meta.Release.Source != "Blu-ray 3D" || meta.ResolvedNaming.Source != "Blu-ray 3D" || meta.Is3D != measured {
				t.Fatal("naming changed canonical source or measured 3D")
			}
		})
	}
}
