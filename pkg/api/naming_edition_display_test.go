// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestEditionDisplayLabelDoesNotChangeCanonicalLabel(t *testing.T) {
	t.Parallel()
	for _, display := range []string{"", " ", "4K Remaster Collector's"} {
		subject := UploadSubject{
			Cut:            "Extended",
			Edition:        "Collector's",
			Presentation:   "Open Matte",
			EditionDisplay: []EditionDisplayPart{{Text: display}},
		}
		if got := subject.EditionLabel(); got != "Extended Collector's Open Matte" {
			t.Fatalf("canonical edition = %q for display %q", got, display)
		}
		want := "Extended Collector's Open Matte"
		if display == "4K Remaster Collector's" {
			want = "Extended 4K Remaster Collector's Open Matte"
		}
		if got := subject.EditionDisplayLabel(); got != want {
			t.Fatalf("display edition = %q, want %q", got, want)
		}
	}
	subject := UploadSubject{
		EditionDisplay: []EditionDisplayPart{{Text: "4K Restoration", Feature: ReleaseFeature4KRestoration}},
		ReleaseName:    "Director's Cut",
	}
	if subject.EditionLabel() != "" || subject.EditionDisplayLabel() != "4K Restoration" {
		t.Fatalf("annotation became canonical edition: %#v", subject)
	}
}

func TestEditionDisplayTextPreservesOrderedWording(t *testing.T) {
	t.Parallel()
	parts := []EditionDisplayPart{
		{Text: " 4K Remastered ", Feature: ReleaseFeature4KRemaster},
		{Text: "Collector's"},
		{Text: " ", Feature: ReleaseFeatureHybrid},
		{Text: " Extras", Feature: ReleaseFeatureExtras},
	}
	if got := EditionDisplayText(parts); got != "4K Remastered Collector's Extras" {
		t.Fatalf("ordered display = %q", got)
	}
	if got := EditionDisplayText(nil); got != "" {
		t.Fatalf("missing display = %q", got)
	}
}
