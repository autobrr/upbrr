// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRegistryProjectsDVLStructuredName(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Uncut JAPANESE 480p DVD Tales",
		Year:        2001,
		Resolution:  "480p",
		Source:      "DVD",
		Edition:     "Uncut",
		Audio:       "DD 2.0",
		VideoEncode: "x264",
		Tag:         "-GRP",
	}, nil)
	subject := api.UploadSubject{
		ReleaseName:      generated.Name,
		ReleaseNameNoTag: generated.NameNoTag,
		GeneratedName:    generated.GeneratedName,
		Type:             "ENCODE",
		Source:           "DVD",
		Edition:          "Uncut",
		Audio:            "DD 2.0",
		VideoEncode:      "x264",
		AudioLanguages:   []string{"Japanese"},
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 4242},
		Release: api.ReleaseInfo{
			Title:      "Uncut JAPANESE 480p DVD Tales",
			Year:       2001,
			Resolution: "480p",
		},
	}
	original := subject.GeneratedName.Clone()
	projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
		Tracker: "DVL",
		Meta:    subject,
	}, "", "", "")
	if failure != nil {
		t.Fatalf("project DVL: %v", failure)
	}
	const want = "Uncut JAPANESE 480p DVD Tales 2001 JAPANESE 480p DVDRip DD 2.0 x264-GRP"
	if projection.UploadReleaseName != want || projection.DuplicateCriteria.Name != want {
		t.Fatalf("names = upload %q search %q, want %q", projection.UploadReleaseName, projection.DuplicateCriteria.Name, want)
	}
	if !reflect.DeepEqual(original, subject.GeneratedName) || subject.ReleaseName != generated.Name {
		t.Fatal("DVL projection mutated canonical naming")
	}
	descriptor, ok := registry.LookupDescriptor("DVL")
	if !ok || descriptor.ReleaseNamePolicy.ID != "unit3d/dvl/v2" || descriptor.ReleaseNamePolicy.Structured == nil {
		t.Fatal("DVL must register its v2 structured policy")
	}
}
