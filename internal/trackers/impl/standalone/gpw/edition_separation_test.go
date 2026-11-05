// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestHybridRetainsRemasterTitle(t *testing.T) {
	subject := api.UploadSubject{Cut: "Director's Cut", ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid}}
	if key, custom := editionFields(subject); key != "director_s_cut" || custom != "Hybrid" {
		t.Fatalf("remaster key=%q custom=%q", key, custom)
	}
	if subject.EditionLabel() != "Director's Cut" {
		t.Fatal("Hybrid entered canonical edition label")
	}
}

func TestEditionDisplayCannotRestoreClearedFeatures(t *testing.T) {
	for _, cut := range []string{"Extended Cut", "Directors Cut", "Director’s Cut"} {
		want := "director_s_cut"
		if cut == "Extended Cut" {
			want = "extended_edition"
		}
		if key, custom := editionFields(api.UploadSubject{Cut: cut}); key != want || custom != "" {
			t.Fatalf("canonical producer cut=%q key=%q custom=%q", cut, key, custom)
		}
	}
	subject := api.UploadSubject{Edition: "Extended Archive Label", EditionDisplay: []api.EditionDisplayPart{
		{Text: "Hybrid", Feature: api.ReleaseFeatureHybrid}, {Text: "Remastered", Feature: api.ReleaseFeatureRemaster}, {Text: "Archive wording"},
	}}
	if key, custom := editionFields(subject); key != "" || custom != "Extended Archive Label / Archive wording" {
		t.Fatalf("opaque/display text promoted or cleared features restored: key=%q custom=%q", key, custom)
	}
	subject.Cut = "Extended"
	subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature4KRestoration, api.ReleaseFeature4KRestoration}
	if key, _ := editionFields(subject); key != "extended_edition / 4k_restoration" {
		t.Fatalf("canonical edition keys=%q", key)
	}
}

func TestEditionDisplayOmitsCanonicalWordingAndSeparators(t *testing.T) {
	subject := api.UploadSubject{
		Edition:         "Criterion",
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRemaster},
		EditionDisplay: []api.EditionDisplayPart{
			{Text: "Criterion"}, {Text: "/"}, {Text: "Remastered", Feature: api.ReleaseFeatureRemaster}, {Text: "Archive wording"},
		},
	}
	if key, custom := editionFields(subject); key != "the_criterion_collection / remaster" || custom != "Archive wording" {
		t.Fatalf("canonical wording repeated in custom edition: key=%q custom=%q", key, custom)
	}
}
