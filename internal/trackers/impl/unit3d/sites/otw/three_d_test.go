// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"reflect"
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

func TestOTWThreeDSourceAliasProjection(t *testing.T) {
	for _, test := range []struct {
		name     string
		detected string
		features []api.ReleaseFeature
		control  *bool
		omitted  bool
		source   *string
		count    int
	}{
		{name: "source-only Auto", count: 1},
		{
			name:     "measured",
			detected: "3D",
			count:    1,
		},
		{
			name:     "subtype Auto",
			features: []api.ReleaseFeature{api.ReleaseFeature3DHalfOU},
			count:    1,
		},
		{name: "explicit No without component", control: new(false)},
		{
			name:     "manual 3D omission",
			detected: "3D",
			omitted:  true,
		},
		{
			name:     "unprojected manual Source",
			detected: "3D",
			source:   new("Handwritten Source"),
			count:    1,
		},
		{
			name:     "unprojected manual Source omission",
			detected: "3D",
			source:   new(""),
			count:    1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.UploadSubject{
				Type:            "DISC",
				Source:          "Blu-ray 3D",
				Is3D:            test.detected,
				ReleaseFeatures: test.features,
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					ThreeD: test.control,
				},
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Example Movie",
					Year:       2026,
					Resolution: "1080p",
					Source:     "Blu-ray 3D",
				},
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
					},
				},
			}
			subject.DiscType = "BDMV"
			if test.omitted {
				subject.GeneratedName.Components = append(subject.GeneratedName.Components, api.ReleaseNameComponent{
					Role:           api.NameRoleThreeD,
					AvailableValue: "3D",
					Manual:         true,
				})
			}
			if test.source != nil {
				subject.GeneratedName.Components = append(subject.GeneratedName.Components, api.ReleaseNameComponent{
					Role:           api.NameRoleSource,
					Value:          *test.source,
					AvailableValue: "Blu-ray 3D",
					Present:        *test.source != "",
					Manual:         true,
				})
			}
			rendered := subject.GeneratedName.Render()
			document := subject.GeneratedName.Clone()
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
						t.Fatalf("complete name changed: %q", name)
					}
				} else if strings.Count(name, "3D") != test.count {
					t.Fatalf("upload=%q, want %d markers", name, test.count)
				}
				if requested == nil && (!strings.Contains(name, "Blu-ray") || strings.Contains(name, "Handwritten Source")) {
					t.Fatalf("legacy upload changed source-component handling: %q", name)
				}
				search := prepared.Projection.DuplicateCriteria.Name
				if strings.Count(search, "3D") != test.count {
					t.Fatalf("search=%q, want %d markers", search, test.count)
				}
				if !strings.Contains(search, "Blu-ray") || strings.Contains(search, "Handwritten Source") {
					t.Fatalf("legacy search changed source-component handling: %q", search)
				}
			}
			if subject.Source != "Blu-ray 3D" || subject.Release.Source != "Blu-ray 3D" || subject.Is3D != test.detected ||
				len(subject.ReleaseFeatures) != len(test.features) ||
				len(subject.ReleaseFeatures) > 0 && subject.ReleaseFeatures[0] != api.ReleaseFeature3DHalfOU ||
				!reflect.DeepEqual(subject.GeneratedName, document) {
				t.Fatal("naming changed retained facts or manual components")
			}
		})
	}
}
