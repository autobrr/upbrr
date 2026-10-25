// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestThreeDNameKeepsSubtypeOutOfEditionOnlyForNaming(t *testing.T) {
	features := []ReleaseFeature{ReleaseFeature3DFullSBS, ReleaseFeatureHybrid}
	parts := []EditionDisplayPart{{Text: "Director's Cut"}, {Text: "3D Full SBS", Feature: ReleaseFeature3DFullSBS}, {Text: "4K Restoration", Feature: ReleaseFeature4KRestoration}}
	for _, detected := range []string{"", "3D"} {
		if got := ThreeDName(detected, features); got != "3D" {
			t.Fatal(got)
		}
	}
	if got := EditionNamingText(parts); got != "Director's Cut 4K Restoration" {
		t.Fatal(got)
	}
	if got := EditionDisplayText(parts); got != "Director's Cut 3D Full SBS 4K Restoration" {
		t.Fatal("original display changed:", got)
	}
	if IsThreeDSubtype(ReleaseFeature2D3DEdition) {
		t.Fatal("mixed edition set treated as a subtype")
	}
}

func TestEditionNamingTextRemovesOrphanedSubtypeSeparators(t *testing.T) {
	parts := []EditionDisplayPart{{Text: "Director's Cut"}, {Text: "/"}, {Text: "3D Full SBS", Feature: ReleaseFeature3DFullSBS}, {Text: "/"}, {Text: "Hybrid", Feature: ReleaseFeatureHybrid}}
	if got := EditionNamingText(parts); got != "Director's Cut / Hybrid" {
		t.Fatal(got)
	}
}

func TestEditionDisplayWithoutThreeDPreservesOtherParts(t *testing.T) {
	for _, test := range []struct {
		name  string
		parts []EditionDisplayPart
		want  string
	}{
		{"leading", []EditionDisplayPart{{Text: "3D Full SBS", Feature: ReleaseFeature3DFullSBS}, {Text: "/"}, {Text: "Rifftrax"}}, "Rifftrax"},
		{"trailing", []EditionDisplayPart{{Text: "Rifftrax"}, {Text: "/"}, {Text: "3D Half OU", Feature: ReleaseFeature3DHalfOU}}, "Rifftrax"},
		{"middle", []EditionDisplayPart{{Text: "Director's Cut"}, {Text: "/"}, {Text: "3D Full SBS", Feature: ReleaseFeature3DFullSBS}, {Text: "/"}, {Text: "Hybrid", Feature: ReleaseFeatureHybrid}}, "Director's Cut / Hybrid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := EditionDisplayText(test.parts)
			got := EditionDisplayWithoutThreeD(test.parts)
			if EditionDisplayText(got) != test.want || EditionDisplayText(test.parts) != original {
				t.Fatalf("filtered = %+v, original = %+v", got, test.parts)
			}
			for _, part := range got {
				if part.Text == "Hybrid" && part.Feature != ReleaseFeatureHybrid {
					t.Fatal("unrelated typed feature lost")
				}
			}
			got[0].Text = "changed"
			if EditionDisplayText(test.parts) != original {
				t.Fatal("filtered display aliases original evidence")
			}
		})
	}
}

func TestThreeDNameUsesGenericMarkerWithoutChangingSubtypeFacts(t *testing.T) {
	for _, feature := range []ReleaseFeature{ReleaseFeature3DAnaglyph, ReleaseFeature3DFullSBS, ReleaseFeature3DHalfOU, ReleaseFeature3DHalfSBS} {
		t.Run(string(feature), func(t *testing.T) {
			features := []ReleaseFeature{feature, ReleaseFeatureHybrid, feature}
			for _, detected := range []string{"", "3D", "HSBS", "3D Half OU"} {
				if got := ThreeDName(detected, features); got != "3D" {
					t.Fatalf("marker = %q", got)
				}
			}
			if len(features) != 3 || features[0] != feature || features[1] != ReleaseFeatureHybrid || features[2] != feature {
				t.Fatal("naming changed subtype evidence")
			}
		})
	}
	if got := ThreeDName(" ", []ReleaseFeature{ReleaseFeature2D3DEdition, ReleaseFeatureHybrid}); got != "" {
		t.Fatalf("unrelated features established 3D: %q", got)
	}
	if got := ThreeDName("HSBS", nil); got != "3D" {
		t.Fatalf("measured subtype leaked into name: %q", got)
	}
}
