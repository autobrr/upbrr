// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestOTWThreeDSubtypePlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:   "3D",
		Type:   "ENCODE",
		Source: "BluRay",
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2026,
			Resolution: "1080p",
		},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfOU},
	}
	got := buildName(subject, config.TrackerConfig{})
	if !strings.Contains(got, "1080p 3D") || strings.Count(got, "3D") != 1 || strings.Contains(got, "Half OU") {
		t.Fatal(got)
	}
}

func TestOTWThreeDManualOmissionSurvivesReviewedProjection(t *testing.T) {
	for _, manual := range []bool{false, true} {
		subject := api.UploadSubject{
			Type:   "ENCODE",
			Source: "BluRay",
			Release: api.ReleaseInfo{
				Title:      "Example Movie",
				Year:       2026,
				Resolution: "1080p",
			},
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
		for _, requested := range []*string{nil, new("Exact Manual Name")} {
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
				Tracker:             "OTW",
				Meta:                subject,
				RequestedUploadName: requested,
			}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
			if failure != nil {
				t.Fatal(failure)
			}
			name, err := prepared.ReviewedUploadName()
			if err != nil {
				t.Fatal(err)
			}
			if requested != nil {
				if name != *requested {
					t.Fatalf("complete override changed: %q", name)
				}
			} else if strings.Contains(name, "3D") == manual {
				t.Fatalf("manual=%t upload=%q", manual, name)
			}
			if strings.Contains(prepared.Projection.DuplicateCriteria.Name, "3D") == manual {
				t.Fatalf("manual=%t search=%q", manual, prepared.Projection.DuplicateCriteria.Name)
			}
			component, _ := subject.GeneratedName.Component(api.NameRoleThreeD)
			if len(subject.ReleaseFeatures) != 1 || component.Manual != manual {
				t.Fatal("projection changed retained facts or manual intent")
			}
		}
	}
}
