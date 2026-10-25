// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPSourceBlockOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, resolution, service, releaseType, source, marker, want string
		omitResolution                                               bool
	}{
		{
			name:           "web",
			resolution:     "1080p",
			service:        "DSNP",
			releaseType:    "WEBRIP",
			source:         "Web",
			marker:         "DS4K",
			want:           "Example Series S01 1080p DS4K Remastered 3D DSNP WEBRip x265-GRP",
			omitResolution: false,
		},
		{
			name:           "absent resolution",
			resolution:     "",
			service:        "DSNP",
			releaseType:    "WEBRIP",
			source:         "Web",
			marker:         "DS4K",
			want:           "Example Series S01 DS4K Remastered 3D DSNP WEBRip x265-GRP",
			omitResolution: false,
		},
		{
			name:           "omitted resolution",
			resolution:     "1080p",
			service:        "DSNP",
			releaseType:    "WEBRIP",
			source:         "Web",
			marker:         "DS4K",
			want:           "Example Series S01 DS4K Remastered 3D DSNP WEBRip x265-GRP",
			omitResolution: true,
		},
		{
			name:           "absent service",
			resolution:     "",
			service:        "",
			releaseType:    "WEBRIP",
			source:         "Web",
			marker:         "DS4K",
			want:           "Example Series S01 DS4K Remastered 3D WEBRip x265-GRP",
			omitResolution: false,
		},
		{
			name:           "broadcast",
			resolution:     "",
			service:        "",
			releaseType:    "HDTV",
			source:         "HDTV",
			marker:         "DS4K",
			want:           "Example Series S01 DS4K Remastered 3D HDTV x265-GRP",
			omitResolution: false,
		},
		{
			name:           "no downscale",
			resolution:     "1080p",
			service:        "DSNP",
			releaseType:    "WEBRIP",
			source:         "Web",
			marker:         "",
			want:           "Example Series S01 1080p Remastered 3D DSNP WEBRip x265-GRP",
			omitResolution: false,
		},
		{
			name:           "disc source encode",
			resolution:     "1080p",
			service:        "",
			releaseType:    "ENCODE",
			source:         "BluRay",
			marker:         "DS4K",
			want:           "Example Series S01 1080p Remastered 3D BluRay x265-GRP",
			omitResolution: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "TV",
				Type:        test.releaseType,
				Title:       "Example Series",
				Season:      "S01",
				Resolution:  test.resolution,
				Downscale:   test.marker,
				Edition:     "Remastered",
				ThreeD:      "3D",
				Source:      test.source,
				Service:     test.service,
				VideoEncode: "x265",
				Tag:         "-GRP",
			})
			subject.Type, subject.Source = test.releaseType, test.source
			if test.omitResolution {
				for index := range subject.GeneratedName.Components {
					component := &subject.GeneratedName.Components[index]
					if component.Role == api.NameRoleResolution {
						component.Present = false
						component.Manual = true
					}
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			subject.ReleaseNameNoTag = subject.GeneratedName.Render().NameNoTag
			canonical := subject.ReleaseName
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: subject}, namePolicy())
			if failure != nil {
				t.Fatal(failure)
			}
			got, err := prepared.ReviewedUploadName()
			if err != nil || got != test.want || prepared.Projection.DuplicateCriteria.Name != got {
				t.Fatalf("reviewed=%q duplicate=%q error=%v; want %q", got, prepared.Projection.DuplicateCriteria.Name, err, test.want)
			}
			if subject.GeneratedName.Render().Name != canonical {
				t.Fatal("projection mutated the canonical name")
			}
			override := "Exact User Name-GRP"
			if got := dpReviewedName(t, subject, &override); got != override {
				t.Fatalf("opaque override changed: %q", got)
			}
			if test.marker != "" {
				markDPManual(t, subject.GeneratedName, api.NameRoleDownscale)
				if got := dpReviewedName(t, subject, nil); strings.Contains(got, "DS4K") {
					t.Fatalf("manual downscale omission changed: %q", got)
				}
			}
		})
	}
}

func TestDPSourceBlockPreservesIndependentFacts(t *testing.T) {
	t.Parallel()
	for _, releaseType := range []string{"WEBRIP", "ENCODE", "REMUX", "DISC"} {
		t.Run(releaseType, func(t *testing.T) {
			source, discType, region := "BluRay", "", ""
			if releaseType == "WEBRIP" {
				source = "Web"
			}
			if releaseType == "DISC" {
				discType = "BDMV"
			}
			if releaseType == "DISC" || releaseType == "REMUX" {
				region = "GER"
			}
			subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "TV",
				Type:        releaseType,
				DiscType:    discType,
				Title:       "Example Series",
				Season:      "S01",
				Cut:         "Extended Cut",
				Hybrid:      true,
				Edition:     "Remastered",
				Resolution:  "1080p",
				Downscale:   "DS4K",
				ThreeD:      "3D",
				Region:      region,
				Source:      source,
				Service:     "DSNP",
				VideoEncode: "x265",
				VideoCodec:  "AVC",
				Tag:         "-GRP",
			})
			subject.Type, subject.Source, subject.DiscType = releaseType, source, discType
			name := dpReviewedName(t, subject, nil)
			block := "1080p Remastered 3D BluRay"
			switch releaseType {
			case "WEBRIP":
				block = "1080p DS4K Remastered 3D DSNP WEBRip"
			case "DISC", "REMUX":
				block = "1080p Remastered GER 3D BluRay"
			}
			if !strings.Contains(name, block) {
				t.Fatalf("missing ordered block %q: %q", block, name)
			}
			for _, fact := range []string{"Extended Cut", "Hybrid"} {
				if strings.Count(name, fact) != 1 || strings.Index(name, fact) > strings.Index(name, "1080p") {
					t.Fatalf("independent fact %q lost or moved into source block: %q", fact, name)
				}
			}
			if releaseType != "WEBRIP" && strings.Contains(name, "DS4K") {
				t.Fatalf("disc-source marker was included: %q", name)
			}
			for index := range subject.GeneratedName.Components {
				component := &subject.GeneratedName.Components[index]
				if component.Role == api.NameRoleEdition || component.Role == api.NameRoleThreeD {
					component.Present, component.Manual = true, true
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			subject.ReleaseNameNoTag = subject.GeneratedName.Render().NameNoTag
			manualName := dpReviewedName(t, subject, nil)
			for _, fact := range []string{"Extended Cut", "Hybrid"} {
				if strings.Count(manualName, fact) != 1 || strings.Index(manualName, fact) > strings.Index(manualName, "1080p") {
					t.Fatalf("manual component moved independent fact %q: %q", fact, manualName)
				}
			}
			if strings.Count(manualName, "Remastered") != 1 || strings.Count(manualName, "3D") != 1 {
				t.Fatalf("present manual components changed: %q", manualName)
			}
			for _, role := range []api.ReleaseNameRole{api.NameRoleEdition, api.NameRoleThreeD} {
				for index := range subject.GeneratedName.Components {
					component := &subject.GeneratedName.Components[index]
					if component.Role == role {
						component.Present, component.Manual = false, true
					}
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			subject.ReleaseNameNoTag = subject.GeneratedName.Render().NameNoTag
			if got := dpReviewedName(t, subject, nil); strings.Contains(got, "Remastered") || strings.Contains(got, "3D") {
				t.Fatalf("manual component omission changed: %q", got)
			}
		})
	}
}
