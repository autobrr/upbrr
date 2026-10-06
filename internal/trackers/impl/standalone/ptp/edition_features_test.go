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
	preview := buildUploadPreview(uploadState{fields: fields}, meta, api.WorkflowExecutionModeNormal)
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
