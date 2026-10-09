// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/yus"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTVDBQualifiersPreserveManualYear(t *testing.T) {
	for _, site := range []struct {
		name       string
		profile    unit3d.Profile
		manualYear string
		automatic  string
	}{
		{"AITHER", aither.Profile(), "TMDB Series 2026 AKA Original US", "TMDB Series AKA Original US"},
		{"DP", dp.Profile(), "TMDB Series 2026 AKA Original US", "TMDB Series AKA Original US"},
		{"HHD", hhd.Profile(), "IMDb Series AKA Original US 2026", "IMDb Series AKA Original US"},
		{"ULCX", ulcx.Profile(), "IMDb Series AKA Original US 2026", "IMDb Series AKA Original US"},
		{"YUS", yus.Profile(), "IMDb Series AKA Original US 2026", "IMDb Series AKA Original US"},
		{"LUME", lume.Profile(), "TMDB Series AKA Original 2026", "TMDB Series AKA Original"},
	} {
		t.Run(site.name, func(t *testing.T) {
			for _, authority := range []string{"automatic", "manual metadata", "manual component", "manual empty metadata"} {
				t.Run(authority, func(t *testing.T) {
					generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
						Category:    "TV",
						Type:        "WEBDL",
						Title:       "Selected Series",
						AltTitle:    "AKA Original",
						Year:        2026,
						SearchYear:  "2026",
						Season:      "S01",
						Episode:     "E02",
						Resolution:  "1080p",
						VideoEncode: "H.265",
						Tag:         "-GRP",
					}, api.NopLogger{})
					if generated.GeneratedName == nil {
						t.Fatal("missing structured name")
					}
					subject := api.UploadSubject{
						SourcePath:    "source",
						GeneratedName: generated.GeneratedName,
						Type:          "WEBDL",
						LanguageFacts: api.LanguageFacts{AudioAbsent: true},
						Identity: api.ExternalIdentity{
							SourcePath: "source",
							Generation: 1,
							Category:   api.CanonicalCategoryTV,
							TVDBID:     1,
							TMDBID:     2,
							IMDBID:     3,
						},
						ProviderMetadata: api.SourceScopedMetadata{
							SourcePath: "source",
							Generation: 1,
							TMDB: &api.TMDBMetadata{
								TMDBID:   2,
								Category: "TV",
								Title:    "TMDB Series",
							},
							IMDB: &api.IMDBMetadata{IMDBID: 3, Title: "IMDb Series"},
							TVDB: &api.TVDBMetadata{
								TVDBID: 1,
								NameDisambiguation: api.TVDBNameDisambiguation{
									CanonicalName: "TVDB Series",
									SeriesYear:    2030,
									Locale:        "US",
									IncludeLocale: true,
									Status:        api.MetadataEvidenceStatusComplete,
									Source:        "tvdb-name/v1",
								},
							},
						},
					}
					want := site.manualYear
					switch authority {
					case "automatic":
						want = site.automatic
					case "manual metadata":
						subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManual
						subject.EffectiveMetadata.Year = 2026
					case "manual component":
						for index := range subject.GeneratedName.Components {
							if subject.GeneratedName.Components[index].Role == api.NameRoleYear {
								subject.GeneratedName.Components[index].Manual = true
							}
						}
					case "manual empty metadata":
						subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManualEmpty
						subject.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = true
						for index := range subject.GeneratedName.Components {
							if subject.GeneratedName.Components[index].Role == api.NameRoleYear {
								subject.GeneratedName.Components[index].Present = false
							}
						}
						want = site.automatic
					}
					subject.ReleaseName = subject.GeneratedName.Render().Name
					prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
						Tracker: site.name,
						Meta:    subject,
					}, unit3d.NewWithProfile(site.profile).ReleaseNamePolicy())
					if failure != nil {
						t.Fatal(failure)
					}
					name, err := prepared.ReviewedUploadName()
					if err != nil {
						t.Fatal(err)
					}
					want += " S01E02 1080p WEB-DL H.265-GRP"
					if name != want {
						t.Fatalf("name = %q, want %q", name, want)
					}
				})
			}
		})
	}
}
