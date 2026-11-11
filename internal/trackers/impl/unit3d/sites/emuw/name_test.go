// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package emuw_test

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/emuw"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ttr"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTVYearPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		episode  string
		mode     string
		want     string
	}{
		{"episode", "TV", "E02", "", "Example Series S01E02 2020 1080p WEB-DL H.265-GRP"},
		{"season pack", "TV", "", "", "Example Series S01 2020 1080p WEB-DL H.265-GRP"},
		{"movie unchanged", "MOVIE", "", "", "Example Series 2020 1080p WEB-DL H.265-GRP"},
		{"missing provider year", "TV", "E02", "missing year", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
		{"existing automatic year", "TV", "E02", "existing year", "Example Series S01E02 2026 1080p WEB-DL H.265-GRP"},
		{"manual year", "TV", "E02", "manual", "Example Series 2026 S01E02 1080p WEB-DL H.265-GRP"},
		{"manual omission", "TV", "E02", "omit", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
		{"complete override", "TV", "E02", "override", "Custom Complete Name"},
		{"stale provider", "TV", "E02", "stale", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
		{"different provider identity", "TV", "E02", "wrong identity", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
		{"different provider category", "TV", "E02", "wrong category", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
		{"TTR unchanged", "TV", "E02", "TTR", "Example Series S01E02 1080p WEB-DL H.265-GRP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := api.ReleaseNameRequest{
				Category:    tc.category,
				Type:        "WEBDL",
				Title:       "Example Series",
				Year:        2026,
				Season:      "S01",
				Episode:     tc.episode,
				Resolution:  "1080p",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			}
			if tc.category == "MOVIE" {
				request.Season = ""
				request.SearchYear = "2026"
			}
			if tc.mode == "existing year" {
				request.SearchYear = "2026"
			}
			generated := metadata.BuildReleaseName(request, api.NopLogger{})
			subject := api.UploadSubject{
				SourcePath:    "source",
				GeneratedName: generated.GeneratedName,
				Identity: api.ExternalIdentity{
					SourcePath: "source",
					Generation: 1,
					Category:   api.CanonicalCategory(strings.ToLower(tc.category)),
					TMDBID:     2,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: "source",
					Generation: 1,
					TMDB: &api.TMDBMetadata{
						TMDBID:   2,
						Category: tc.category,
						Title:    "Example Series",
						Year:     2020,
					},
				},
			}
			switch tc.mode {
			case "missing year", "existing year":
				subject.ProviderMetadata.TMDB.Year = 0
			case "stale", "wrong identity", "wrong category":
				// Manual title bypasses title-provider validation, so year evidence must
				// independently match the prepared identity and generation.
				for i := range subject.GeneratedName.Components {
					if subject.GeneratedName.Components[i].Role == api.NameRoleTitle {
						subject.GeneratedName.Components[i].Manual = true
					}
				}
				switch tc.mode {
				case "stale":
					subject.ProviderMetadata.Generation++
				case "wrong identity":
					subject.ProviderMetadata.TMDB.TMDBID++
				case "wrong category":
					subject.ProviderMetadata.TMDB.Category = "MOVIE"
				}
			case "manual", "omit":
				subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManual
				for i := range subject.GeneratedName.Components {
					c := &subject.GeneratedName.Components[i]
					if c.Role == api.NameRoleYear {
						c.Manual = true
						c.Value = "2026"
						c.Present = tc.mode == "manual"
					}
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			if tc.mode == "override" {
				subject.ReleaseName = tc.want
			}
			profile := emuw.Profile()
			if tc.mode == "TTR" {
				profile = ttr.Profile()
			}
			original := subject.GeneratedName.Render().Name
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: profile.Name, Meta: subject}, unit3d.NewWithProfile(profile).ReleaseNamePolicy())
			if failure != nil {
				t.Fatal(failure)
			}
			name, err := prepared.ReviewedUploadName()
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Projection.DuplicateCriteria.Name != name {
				t.Fatalf("search name = %q, upload name = %q", prepared.Projection.DuplicateCriteria.Name, name)
			}
			if subject.GeneratedName.Render().Name != original {
				t.Fatal("policy mutated the canonical name")
			}
			if name != tc.want {
				t.Fatalf("name = %q, want %q", name, tc.want)
			}
		})
	}
}
