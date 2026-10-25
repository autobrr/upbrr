// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedHDBThreeDPlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:   "3D",
		Type:   "ENCODE",
		Source: "BluRay",
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Year:       2026,
			Resolution: "1080p",
		},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS},
	}
	name := buildGeneratedHDBName(subject, "Example Movie")
	if !strings.Contains(name, "1080p 3D") || strings.Count(name, "3D") != 1 {
		t.Fatal(name)
	}
}

func TestGeneratedHDBThreeDPlacementAfterRegion(t *testing.T) {
	for _, test := range []struct{ kind, disc, region, want string }{
		{"DISC", "BDMV", "CEE", "1080p CEE 3D Blu-ray"},
		{"REMUX", "", "USA", "1080p USA 3D Remux"},
		{"ENCODE", "", "USA", "1080p USA 3D BluRay"},
		{"ENCODE", "", "", "1080p 3D BluRay"},
	} {
		subject := api.UploadSubject{
			Is3D:     "3D",
			Type:     test.kind,
			DiscType: test.disc,
			Region:   test.region,
			Source:   "BluRay",
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Year:       2026,
				Resolution: "1080p",
			},
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS},
		}
		if got := buildGeneratedHDBName(subject, "Example Movie"); got != "Example Movie 2026 "+test.want {
			t.Fatalf("%s name = %q, want tail %q", test.kind, got, test.want)
		}
	}
}

func TestHDBSourceElementPreservesNonThreeDRegionOrder(t *testing.T) {
	for _, test := range []struct{ kind, source, want string }{
		{"ENCODE", "BluRay", "BluRay"},
		{"WEBDL", "WEB", "WEB-DL"},
		{"WEBRIP", "WEB", "WEBRip"},
		{"DVDRIP", "DVD", "DVDRip"},
		{"HDTV", "", "HDTV"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			subject := api.UploadSubject{
				Type:   test.kind,
				Source: test.source,
				Region: "USA",
			}
			if got := hdbSourceElement(subject); got != test.want+" USA" {
				t.Fatalf("non-3D source = %q", got)
			}
			subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS}
			if got := hdbSourceElement(subject); got != "USA 3D "+test.want {
				t.Fatalf("subtype source = %q", got)
			}
			subject.ReleaseFeatures = nil
			subject.Is3D = "3D"
			if got := hdbSourceElement(subject); got != "USA 3D "+test.want {
				t.Fatalf("generic 3D source = %q", got)
			}
		})
	}
}

func TestHDBThreeDManualOmissionPreservesSubtypeFacts(t *testing.T) {
	for _, manual := range []bool{false, true} {
		subject := api.UploadSubject{
			Is3D: "3D",
			Release: api.ReleaseInfo{
				Title:      "Example Movie",
				Year:       2026,
				Resolution: "1080p",
				Category:   "MOVIE",
			},
			Type: "ENCODE",
			EffectiveMetadata: api.EffectiveMetadata{
				OriginalTitle:           "Example Movie",
				OriginalTitleProvenance: api.FactProvenanceManual,
			},
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
			Tracker: "HDB",
			Meta:    subject,
		}, Profile().ReleaseNamePolicy)
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

func TestHDBThreeDSourceAliasProjection(t *testing.T) {
	for _, test := range []struct {
		name     string
		detected string
		features []api.ReleaseFeature
		control  *bool
		omitted  bool
		source   *string
		want     string
	}{
		{name: "source-only Auto", want: "Blu-ray 3D"},
		{
			name:     "measured",
			detected: "3D",
			want:     "3D BluRay",
		},
		{
			name:     "subtype Auto",
			features: []api.ReleaseFeature{api.ReleaseFeature3DHalfOU},
			want:     "3D BluRay",
		},
		{
			name:    "explicit No without component",
			control: new(false),
			want:    "BluRay",
		},
		{
			name:     "manual 3D omission",
			detected: "3D",
			omitted:  true,
			want:     "BluRay",
		},
		{
			name:     "unprojected manual Source",
			detected: "3D",
			source:   new("Handwritten Source"),
			want:     "3D BluRay",
		},
		{
			name:     "unprojected manual Source omission",
			detected: "3D",
			source:   new(""),
			want:     "3D BluRay",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.UploadSubject{
				Type:            "ENCODE",
				Source:          "Blu-ray 3D",
				Is3D:            test.detected,
				ReleaseFeatures: test.features,
				ReleaseNameOverrides: api.ReleaseNameOverrides{
					ThreeD: test.control,
				},
				EffectiveMetadata: api.EffectiveMetadata{
					OriginalTitle:           "Example Movie",
					OriginalTitleProvenance: api.FactProvenanceManual,
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
			wantName := "Example Movie 2026 1080p " + test.want
			for _, requested := range []*string{nil, new("Exact Manual Name")} {
				prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
					Tracker:             "HDB",
					Meta:                subject,
					RequestedUploadName: requested,
				}, Profile().ReleaseNamePolicy)
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
				} else if name != wantName {
					t.Fatalf("upload=%q, want %q", name, wantName)
				}
				if search := prepared.Projection.DuplicateCriteria.Name; search != wantName {
					t.Fatalf("search=%q, want %q", search, wantName)
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
