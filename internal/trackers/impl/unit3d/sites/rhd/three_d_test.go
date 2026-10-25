// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestRHDThreeDSourcePlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:            "3D",
		Type:            "DISC",
		DiscType:        "BDMV",
		Region:          "CEE",
		Release:         api.ReleaseInfo{Source: "Blu-ray"},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DFullSBS},
	}
	got := strings.Join(typeAndSource(subject), " ")
	if !strings.Contains(got, "CEE 3D Blu-ray") || strings.Count(got, "3D") != 1 {
		t.Fatal(got)
	}
}

func TestRHDThreeDSourcePlacementOtherTypes(t *testing.T) {
	for _, kind := range []string{"HDTV", "DVDRIP", ""} {
		subject := api.UploadSubject{Is3D: "3D", Type: kind}
		got := strings.Join(typeAndSource(subject), " ")
		want := strings.TrimSpace("3D " + kind)
		if got != want {
			t.Fatalf("%q: got %q, want %q", kind, got, want)
		}
	}
}

func TestRHDThreeDManualOmissionPreservesSubtypeFacts(t *testing.T) {
	for _, manual := range []bool{false, true} {
		subject := api.UploadSubject{
			Is3D: "3D",
			Release: api.ReleaseInfo{
				Title:      "Example Movie",
				Year:       2026,
				Resolution: "1080p",
				Category:   "MOVIE",
			},
			Type:            "ENCODE",
			Source:          "BluRay",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfOU},
			GeneratedName: &api.ReleaseNameDocument{
				Version: api.ReleaseNameDocumentVersionV1,
				Components: []api.ReleaseNameComponent{
					{
						Role:           api.NameRoleTitle,
						Value:          "Example Movie",
						AvailableValue: "Example Movie",
						Present:        true,
						Manual:         true,
					},
					{
						Role:           api.NameRoleThreeD,
						AvailableValue: "3D",
						Manual:         manual,
					},
				},
			},
		}
		rendered := subject.GeneratedName.Render()
		subject.ReleaseName = rendered.Name
		subject.GeneratedReleaseNames = api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: rendered,
			OmitEpisodeTitle:    rendered,
		}
		prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
			Tracker: "RHD",
			Meta:    subject,
		}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
		if failure != nil {
			t.Fatal(failure)
		}
		name, err := prepared.ReviewedUploadName()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(name, "3D") == manual || strings.Contains(prepared.Projection.DuplicateCriteria.Name, "3D") == manual {
			t.Fatalf("manual=%t upload/search=%q/%q", manual, name, prepared.Projection.DuplicateCriteria.Name)
		}
		if subject.Is3D != "3D" || len(subject.ReleaseFeatures) != 1 {
			t.Fatal("naming changed retained 3D facts")
		}
	}
}
