// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPTPEditionFeatureCatalogueAndPayloadAgree(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Release:       api.ReleaseInfo{Collection: "Criterion.Collection", Resolution: "1080p"},
		Cut:           "Theatrical / Director's Cut / Extended / Uncut / Unrated",
		Edition:       "Rifftrax / 4K Restoration / 4K Remaster / Extras",
		Presentation:  "2D/3D Edition / 3D Anaglyph / 3D Full SBS / 3D Half OU / 3D Half SBS",
		EditionSet:    "2in1",
		HasCommentary: true,
		Audio:         "Dual Dubbed DTS:X Atmos",
		Type:          "REMUX",
		Source:        "BluRay",
		Container:     "MKV",
		VideoEncode:   "x264",
		HDR:           "DV HDR10+ HLG",
		Disc:          api.DiscFacts{Items: []api.DiscItemFacts{{ID: "disc1"}, {ID: "disc2"}}},
	}
	options := editionFeatures(meta)
	if len(options) != 30 {
		t.Fatalf("catalogue = %#v", options)
	}
	var selected []string
	for _, option := range options {
		if option.Category == "" || option.Evidence == "" {
			t.Fatalf("missing review evidence: %#v", option)
		}
		if option.Selected {
			selected = append(selected, option.Label)
		}
	}
	for _, label := range []string{"The Criterion Collection", "Theatrical Cut", "Director's Cut", "Extended Edition", "Uncut", "Unrated", "Rifftrax", "2in1", "2-Disc Set", "4K Restoration", "4K Remaster", "Extras", "With Commentary", "3D Full SBS", "3D Half SBS", "3D Half OU", "3D Anaglyph", "2D/3D Edition", "Remux", "DTS:X", "Dolby Atmos", "Dual Audio", "English Dub", "Dolby Vision", "HDR10+", "HLG"} {
		if !slices.Contains(selected, label) {
			t.Fatalf("missing %s in %v", label, selected)
		}
	}
	if slices.Contains(selected, "HDR10") || slices.Contains(selected, "10-bit") {
		t.Fatalf("technical exclusivity lost: %v", selected)
	}
	fields, err := buildUploadFields(meta, "synthetic", "1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if fields["remaster_title"] != strings.Join(selected, " / ") || fields["remaster"] != "on" {
		t.Fatalf("payload disagrees: %#v", fields)
	}
	preview := buildUploadPreview(uploadState{fields: fields}, meta)
	if !slices.Equal(preview.EditionFeatures, options) || preview.Payload["remaster_title"] != fields["remaster_title"] {
		t.Fatalf("preview disagrees: %#v", preview)
	}
	if _, exists := fields["edition_features"]; exists {
		t.Fatal("inspection data entered PTP wire fields")
	}
}

func TestPTPEditionFeaturesRequirePreparedEvidence(t *testing.T) {
	t.Parallel()
	for _, meta := range []api.UploadSubject{
		{},
		{Cut: "Theatrical Cut"},
		{Edition: "Theatrical"},
		{Edition: "With Commentary", HasCommentary: false},
		{
			Release:     api.ReleaseInfo{Resolution: "2160p"},
			Is3D:        "3D",
			ReleaseName: "Rifftrax 4K Restoration Extras",
		},
	} {
		if got := resolveRemasterTitle(meta); got != "" {
			t.Fatalf("invented labels %q from %#v", got, meta)
		}
		options := editionFeatures(meta)
		if len(options) != 30 {
			t.Fatalf("missing unselected catalogue: %#v", options)
		}
	}
	for _, test := range []struct {
		subject api.UploadSubject
		want    string
	}{
		{api.UploadSubject{BitDepth: "10"}, "10-bit"},
		{api.UploadSubject{HDR: "HDR10"}, "HDR10"},
		{api.UploadSubject{Edition: "Theatrical / Uncut"}, "Theatrical Cut / Uncut"},
		{api.UploadSubject{Release: api.ReleaseInfo{Collection: "MOC"}}, "Masters of Cinema"},
		{api.UploadSubject{Distributor: "WAC"}, "Warner Archive Collection"},
		{api.UploadSubject{Distributor: "Arrow", Release: api.ReleaseInfo{Collection: "Criterion.Collection"}}, ""},
		{api.UploadSubject{EffectiveMetadata: api.EffectiveMetadata{DistributorProvenance: api.FactProvenanceManualEmpty}, Release: api.ReleaseInfo{Collection: "Criterion.Collection"}}, ""},
		{api.UploadSubject{Edition: "Custom Edition"}, "Custom Edition"},
		{api.UploadSubject{Edition: "Custom Edition+"}, "Custom Edition+"},
	} {
		if got := resolveRemasterTitle(test.subject); got != test.want {
			t.Fatalf("labels = %q, want %q", got, test.want)
		}
	}
}

func TestPTPPreparedFeaturesAreSpecificAndDeduplicated(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		feature api.ReleaseFeature
		label   string
	}{
		{api.ReleaseFeatureTwoDiscSet, "2-Disc Set"},
		{api.ReleaseFeature4KRestoration, "4K Restoration"},
		{api.ReleaseFeature4KRemaster, "4K Remaster"},
		{api.ReleaseFeatureExtras, "Extras"},
		{api.ReleaseFeature2D3DEdition, "2D/3D Edition"},
		{api.ReleaseFeature3DAnaglyph, "3D Anaglyph"},
		{api.ReleaseFeature3DFullSBS, "3D Full SBS"},
		{api.ReleaseFeature3DHalfOU, "3D Half OU"},
		{api.ReleaseFeature3DHalfSBS, "3D Half SBS"},
	} {
		meta := api.UploadSubject{
			Cut:             "Theatrical",
			ReleaseFeatures: []api.ReleaseFeature{test.feature, test.feature},
			Is3D:            "3D",
		}
		if got := resolveRemasterTitle(meta); got != test.label {
			t.Fatalf("feature %s became %q, want %q", test.feature, got, test.label)
		}
		meta.Cut = ""
		meta.Edition = test.label
		if got := resolveRemasterTitle(meta); got != test.label {
			t.Fatalf("manual Edition/feature deduplication = %q", got)
		}
	}
	if got := resolveRemasterTitle(api.UploadSubject{Cut: "Theatrical", Presentation: "Open Matte"}); got != "Open Matte" {
		t.Fatalf("presentation retained theatrical cut: %q", got)
	}
	options := editionFeatures(api.UploadSubject{})
	for _, option := range options {
		if option.Label == "With Commentary" && !strings.Contains(option.Evidence, "false") {
			t.Fatalf("false commentary state unavailable: %#v", option)
		}
	}
}

func TestPTPManualTechnicalLabelsUseCatalogue(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"Remux", "DTS:X", "Dolby Atmos", "Dual Audio", "English Dub", "10-bit", "Dolby Vision", "HDR10+", "HDR10", "HLG"} {
		meta := api.UploadSubject{Edition: label}
		options := editionFeatures(meta)
		if len(options) != len(editionFeatureCatalogue) {
			t.Errorf("%s escaped into an extra edition row", label)
		}
		var selected []string
		for _, option := range options {
			if option.Selected {
				selected = append(selected, option.Label)
				if option.Label == label && option.Category != "Feature" {
					t.Errorf("%s is not selected in feature catalogue", label)
				}
			}
		}
		if !slices.Equal(selected, []string{label}) || resolveRemasterTitle(meta) != label {
			t.Errorf("%s selections = %v", label, selected)
		}
	}
	meta := api.UploadSubject{
		Edition: "HDR10+ / HLG / Remux",
		HDR:     "HDR10+ HLG",
		Type:    "REMUX",
	}
	if got := resolveRemasterTitle(meta); got != "Remux / HDR10+ / HLG" {
		t.Fatalf("manual and detected features diverge or duplicate: %q", got)
	}
}

func TestPTPHybridRemainsDistinctFeature(t *testing.T) {
	t.Parallel()
	subject := api.UploadSubject{
		Release:         api.ReleaseInfo{Resolution: "1080p"},
		Cut:             "Director's Cut",
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid},
	}
	want := "Director's Cut / Hybrid"
	if got := resolveRemasterTitle(subject); got != want {
		t.Fatalf("remaster title=%q, want %q", got, want)
	}
	fields, err := buildUploadFields(subject, "synthetic", "1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	preview := buildUploadPreview(uploadState{fields: fields}, subject)
	if fields["remaster_title"] != want || preview.Payload["remaster_title"] != want {
		t.Fatalf("payload=%q preview=%v, want %q", fields["remaster_title"], preview.Payload["remaster_title"], want)
	}
	for _, option := range editionFeatures(subject) {
		if option.Label == "Hybrid" {
			if !option.Selected || option.Category != "Feature" {
				t.Fatalf("Hybrid=%+v", option)
			}
			return
		}
	}
	t.Fatal("Hybrid missing from feature review")
}

func TestPTPManualHybridWordingPreservesPayload(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ edition, want string }{
		{"Hybrid Uncut", "Uncut / Hybrid"},
		{"Hybrid Custom Label", "Hybrid Custom Label"},
	} {
		t.Run(test.edition, func(t *testing.T) {
			// Final manual overrides clear automatic features and display fragments.
			subject := api.UploadSubject{Edition: test.edition}
			if got := resolveRemasterTitle(subject); got != test.want {
				t.Fatalf("remaster title=%q, want %q", got, test.want)
			}
			for _, option := range editionFeatures(subject) {
				if option.Selected && option.Category == "Feature" {
					t.Fatalf("manual wording inferred a feature: %+v", option)
				}
			}
		})
	}
}

func TestSeparatedFeatureDisplayPreservesPTPLabelsAndPayload(t *testing.T) {
	for _, test := range []struct {
		label   string
		feature api.ReleaseFeature
	}{
		{"Remastered Version", api.ReleaseFeatureRemaster},
		{"Restored", api.ReleaseFeatureRestoration},
		{"4K Restoration", api.ReleaseFeature4KRestoration},
		{"Extras", api.ReleaseFeatureExtras},
	} {
		t.Run(test.label, func(t *testing.T) {
			legacy := api.UploadSubject{Cut: "Director's Cut", Edition: "Archive Label " + test.label + " Festival Label"}
			separated := api.UploadSubject{
				Cut:             "Director's Cut",
				Edition:         "Archive Label Festival Label",
				ReleaseFeatures: []api.ReleaseFeature{test.feature},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "Archive Label"}, {Text: test.label, Feature: test.feature}, {Text: "Festival Label"}},
			}
			if got, want := resolveRemasterTitle(separated), resolveRemasterTitle(legacy); got != want {
				t.Fatalf("payload=%q want legacy=%q", got, want)
			}
			found := false
			for _, option := range editionFeatures(separated) {
				if option.Label == test.label && option.Selected {
					found = true
					if option.Category != "Feature" {
						t.Fatalf("annotation is not a feature: %+v", option)
					}
				}
			}
			if !found {
				t.Fatal("named feature absent from review")
			}
		})
	}
}

func TestPTPDisplayPreservesUnselectedWordingAndSuppression(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		subject    api.UploadSubject
		want       string
		unselected string
	}{
		{
			name: "separate restoration attributes do not imply 4K restoration",
			subject: api.UploadSubject{
				Edition:         "4K",
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRestoration},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "4K"}, {Text: "Restoration", Feature: api.ReleaseFeatureRestoration}},
			},
			want:       "4K Restoration",
			unselected: "4K Restoration",
		},
		{
			name: "separate remaster attributes do not imply 4K remaster",
			subject: api.UploadSubject{
				Edition:         "4K",
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRemaster},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "4K"}, {Text: "Remaster", Feature: api.ReleaseFeatureRemaster}},
			},
			want:       "4K Remaster",
			unselected: "4K Remaster",
		},
		{
			name: "selected collection spanning attributes is not duplicated",
			subject: api.UploadSubject{
				Edition:         "Warner Archive Collection",
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRestoration},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "Warner"}, {Text: "Archive Collection"}, {Text: "Restored", Feature: api.ReleaseFeatureRestoration}},
			},
			want: "Warner Archive Collection / Restored",
		},
		{
			name: "false commentary remains suppressed",
			subject: api.UploadSubject{
				Edition:         "With Commentary",
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRestoration},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "Restored", Feature: api.ReleaseFeatureRestoration}, {Text: "With Commentary"}},
			},
			want:       "Restored",
			unselected: "With Commentary",
		},
		{
			name: "solitary theatrical cut remains suppressed",
			subject: api.UploadSubject{
				Cut:             "Theatrical",
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRestoration},
				EditionDisplay:  []api.EditionDisplayPart{{Text: "Restored", Feature: api.ReleaseFeatureRestoration}},
			},
			want:       "Restored",
			unselected: "Theatrical Cut",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.subject.Release.Resolution = "1080p"
			fields, err := buildUploadFields(test.subject, "synthetic", "1", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			preview := buildUploadPreview(uploadState{fields: fields}, test.subject)
			if fields["remaster_title"] != test.want || preview.Payload["remaster_title"] != test.want {
				t.Errorf("payload=%q preview=%v, want %q", fields["remaster_title"], preview.Payload["remaster_title"], test.want)
			}
			for _, option := range preview.EditionFeatures {
				if option.Label == test.unselected && option.Selected {
					t.Errorf("display wording selected unsupported feature: %+v", option)
				}
			}
		})
	}
}

func TestExplicitFeaturesDoNotInferFromOpaqueEdition(t *testing.T) {
	subject := api.UploadSubject{Edition: "Custom / 4K Restoration", ReleaseNameOverrides: api.ReleaseNameOverrides{ReleaseFeatures: new([]api.ReleaseFeature{})}}
	for _, option := range editionFeatures(subject) {
		if option.Label == "4K Restoration" && option.Selected {
			t.Fatal("None restored a feature from opaque Edition")
		}
	}
	if got := resolveRemasterTitle(subject); got != subject.Edition {
		t.Fatalf("opaque wording changed: %q", got)
	}
	subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature4KRestoration}
	subject.ReleaseNameOverrides.ReleaseFeatures = new([]api.ReleaseFeature{api.ReleaseFeature4KRestoration})
	if got := resolveRemasterTitle(subject); got != "4K Restoration / Custom" {
		t.Fatalf("selected feature duplicated in residual wording: %q", got)
	}
	if !slices.ContainsFunc(editionFeatures(subject), func(option api.TrackerEditionFeature) bool {
		return option.Label == "4K Restoration" && option.Selected
	}) {
		t.Fatal("explicit feature lost")
	}
}
