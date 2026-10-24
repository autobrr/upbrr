// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPDownscaleUsesRetainedSourceEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, releaseType, source, uhd, marker, want string
	}{
		{"web", "WEBRIP", "Web", "", "DS4K", "Example Series S01 1080p DS4K DSNP WEBRip DD+ 5.1 x265-GRP"},
		{"broadcast", "HDTV", "HDTV", "", "DS4K", "Example Series S01 1080p DS4K HDTV DD+ 5.1 x265-GRP"},
		{"unknown", "WEBRIP", "Web", "", "", "Example Series S01 1080p DSNP WEBRip DD+ 5.1 x265-GRP"},
		{"unsupported marker", "WEBRIP", "Web", "", "DS8K", "Example Series S01 1080p DSNP WEBRip DD+ 5.1 x265-GRP"},
		{"disc source downscale", "ENCODE", "BluRay", "UHD", "DS4K", "Example Series S01 1080p UHD BluRay DD+ 5.1 x265-GRP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := api.ReleaseNameRequest{
				Category:    "TV",
				Type:        test.releaseType,
				Title:       "Example Series",
				Season:      "S01",
				Resolution:  "1080p",
				Downscale:   test.marker,
				Source:      test.source,
				UHD:         test.uhd,
				Service:     "DSNP",
				Audio:       "DD+ 5.1",
				VideoEncode: "x265",
				Tag:         "-GRP",
			}
			subject := dpGeneratedSubject(t, request)
			subject.Type, subject.Source = test.releaseType, test.source
			canonical := subject.GeneratedName.Render().Name
			if strings.Contains(canonical, "DS4K") || strings.Contains(canonical, "DS8K") {
				t.Fatalf("hidden marker changed canonical name: %q", canonical)
			}
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: subject}, namePolicy())
			if failure != nil {
				t.Fatal(failure)
			}
			got, err := prepared.ReviewedUploadName()
			if err != nil || got != test.want {
				t.Fatalf("reviewed name = %q, error = %v; want %q", got, err, test.want)
			}
			if prepared.Projection.DuplicateCriteria.Name != got {
				t.Fatalf("duplicate name = %q, upload name = %q", prepared.Projection.DuplicateCriteria.Name, got)
			}
			if subject.GeneratedName.Render().Name != canonical {
				t.Fatal("tracker projection mutated canonical name")
			}
			override := "Exact User Name-GRP"
			if got := dpReviewedName(t, subject, &override); got != override {
				t.Fatalf("opaque override changed: %q", got)
			}
		})
	}
}

func TestDPDownscalePreservesManualOmission(t *testing.T) {
	t.Parallel()
	subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBRIP",
		Title:       "Example Series",
		Season:      "S01",
		Resolution:  "1080p",
		Downscale:   "DS4K",
		Source:      "Web",
		Service:     "DSNP",
		VideoEncode: "x265",
		Tag:         "-GRP",
	})
	subject.Type, subject.Source = "WEBRIP", "Web"
	markDPManual(t, subject.GeneratedName, api.NameRoleDownscale)
	if got := dpReviewedName(t, subject, nil); strings.Contains(got, "DS4K") {
		t.Fatalf("manual omission changed: %q", got)
	}
}
