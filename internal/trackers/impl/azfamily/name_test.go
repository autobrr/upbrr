// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditNameAZCZPolicyFixtures(t *testing.T) {
	tests := []struct {
		name string
		site string
		meta api.UploadSubject
		want string
	}{
		{
			name: "AZ movie uses English title without AKA",
			site: "AZ",
			meta: generatedAZSubject(
				"MOVIE",
				"Example Release Example Native 2026 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Release",
					Alt:   "Example Native",
					Year:  2026,
				},
			),
			want: "Example Release 2026 1080p WEB-DL H.265-GRP",
		},
		{
			name: "AZ single episode omits year and AKA",
			site: "AZ",
			meta: generatedAZSubject(
				"TV",
				"Example Series 2026 Example Native S01E02 Example Episode 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Series",
					Alt:   "Example Native",
					Year:  2026,
				},
			),
			want: "Example Series S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
		{
			name: "AZ season orders season before year",
			site: "AZ",
			meta: generatedAZSubject(
				"TV",
				"Example Series 2026 Example Native S01 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Series",
					Alt:   "Example Native",
					Year:  2026,
				},
			),
			want: "Example Series S01 2026 1080p WEB-DL H.265-GRP",
		},
		{
			name: "CZ uses one English country AKA",
			site: "CZ",
			meta: generatedCZSubject(
				"MOVIE",
				"Example Localized Primer Filma 2026 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Primer Filma",
					Year:  2026,
				},
				&api.IMDBMetadata{
					Title:       "Example Localized",
					AKA:         "Primer Filma",
					Country:     "Exampleland",
					CountryList: "Exampleland, Secondland",
					Akas: []api.IMDBAKA{
						{
							Title:    "Invalid Other Country",
							Country:  "Otherland",
							Language: "English",
						},
						{
							Title:    "Example Film",
							Country:  "Exampleland",
							Language: "English",
						},
						{
							Title:    "Second English Title",
							Country:  "Secondland",
							Language: "English",
						},
					},
				},
			),
			want: "Example Film 2026 1080p WEB-DL H.265-GRP",
		},
		{
			name: "CZ falls back to original without AKA pair",
			site: "CZ",
			meta: generatedCZSubject(
				"MOVIE",
				"Example Localized Primer Filma 2026 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Primer Filma",
					Year:  2026,
				},
				&api.IMDBMetadata{
					Title:   "Example Localized",
					AKA:     "Primer Filma",
					Country: "Exampleland",
				},
			),
			want: "Primer Filma 2026 1080p WEB-DL H.265-GRP",
		},
		{
			name: "CZ transliterates native original title",
			site: "CZ",
			meta: generatedCZSubject(
				"MOVIE",
				"Example Localized Пример Фильм 2026 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Пример Фильм",
					Year:  2026,
				},
				&api.IMDBMetadata{
					Title:   "Example Localized",
					AKA:     "Пример Фильм",
					Country: "Exampleland",
				},
			),
			want: "Primer Film 2026 1080p WEB-DL H.265-GRP",
		},
		{
			name: "CZ TV always includes year",
			site: "CZ",
			meta: generatedCZSubject(
				"TV",
				"Example Localized Primer Serii 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Primer Serii",
					Year:  2026,
				},
				&api.IMDBMetadata{
					Title:   "Example Localized",
					AKA:     "Primer Serii",
					Country: "Exampleland",
				},
			),
			want: "Primer Serii 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
		{
			name: "CZ preserves NoGroup suffix rule",
			site: "CZ",
			meta: generatedCZSubject(
				"MOVIE",
				"Example Localized Primer Filma 2026 1080p WEB-DL H.265-NOGRP",
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Primer Filma",
					Year:  2026,
				},
				&api.IMDBMetadata{
					Title:   "Example Localized",
					AKA:     "Primer Filma",
					Country: "Exampleland",
				},
			),
			want: "Primer Filma 2026 1080p WEB-DL H.265-NoGroup",
		},
		{
			name: "AZ preserves exact source name",
			site: "AZ",
			meta: api.UploadSubject{
				Identity:    api.ExternalIdentity{Category: "MOVIE"},
				ReleaseName: "Exact.Source.Name.2026.Dubbed-GRP",
				Release:     api.ReleaseInfo{Title: "Different Title", Year: 2026},
			},
			want: "Exact.Source.Name.2026.Dubbed-GRP",
		},
		{
			name: "CZ preserves exact scene name",
			site: "CZ",
			meta: api.UploadSubject{
				Identity:    api.ExternalIdentity{Category: "MOVIE"},
				Scene:       true,
				SceneName:   "Exact.Scene.Name.2026-NOGRP",
				ReleaseName: "Generated Replacement 2026-NOGRP",
				Release:     api.ReleaseInfo{Title: "Generated Replacement", Year: 2026},
				Tag:         "-NOGRP",
			},
			want: "Exact.Scene.Name.2026-NOGRP",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := editName(siteFor(test.site), test.meta); got != test.want {
				t.Fatalf("editName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAZFamilyNamingPolicyVersions(t *testing.T) {
	tests := []struct {
		site string
		want string
	}{
		{site: "AZ", want: "azfamily/az/v2"},
		{site: "CZ", want: "azfamily/cz/v2"},
		{site: "PHD", want: "azfamily/phd/v2"},
	}
	for _, test := range tests {
		t.Run(test.site, func(t *testing.T) {
			if got := New(test.site).ReleaseNamePolicy().ID; got != test.want {
				t.Fatalf("policy ID = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAZReleaseNamePolicyPreservesDailyDate(t *testing.T) {
	meta := generatedAZSubject(
		"TV",
		"Example Series 2026-02-03 1080p WEB-DL H.265-GRP",
		api.ReleaseInfo{
			Title: "Example Series",
			Year:  2026,
		},
	)
	meta.DailyEpisodeDate = "2026-02-03"
	meta.NamePresentation = api.ReleaseNamePresentation{
		Version:      api.ReleaseNamePresentationVersionV1,
		UseDailyDate: true,
	}

	input, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "AZ", Meta: meta},
		New("AZ").ReleaseNamePolicy(),
	)
	if failure != nil {
		t.Fatalf("resolve AZ release name: %v", failure)
	}
	if got, want := input.Projection.UploadReleaseName, "Example Series 2026-02-03 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("daily upload name = %q, want %q", got, want)
	}
}

func TestEditPHDNameUploadAssistantParity(t *testing.T) {
	tests := []struct {
		name        string
		releaseName string
		meta        api.UploadSubject
		want        string
	}{
		{
			name:        "encode settings use x264 and x265 labels",
			releaseName: "Example Release 2026 H.264 H.265-GRP",
			meta: api.UploadSubject{
				HasEncodeSettings: true,
				Tag:               "-GRP",
			},
			want: "Example Release 2026 x264 x265-GRP",
		},
		{
			name:        "DVD rip removes source",
			releaseName: "Example Release 2026 DVD DVDRip DD 2.0-GRP",
			meta: api.UploadSubject{
				Type:   "DVDRIP",
				Source: "DVD",
				Tag:    "-GRP",
			},
			want: "Example Release 2026 DVDRip DD 2.0-GRP",
		},
		{
			name:        "DVD replaces region and source and appends codec",
			releaseName: "Example Release 2026 R1 DVD DD 5.1-GRP",
			meta: api.UploadSubject{
				DiscType:   "DVD",
				Region:     "R1",
				Source:     "DVD",
				Audio:      "DD 5.1",
				VideoCodec: "MPEG-2",
				Tag:        "-GRP",
				Release:    api.ReleaseInfo{Resolution: "480p"},
			},
			want: "Example Release 2026 480p DD 5.1 MPEG-2-GRP",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.meta.ReleaseName = test.releaseName
			if got := editName(siteFor("PHD"), test.meta); got != test.want {
				t.Fatalf("editName() = %q, want %q", got, test.want)
			}
		})
	}
}

func generatedAZSubject(category, name string, release api.ReleaseInfo) api.UploadSubject {
	meta := api.UploadSubject{
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategory(category)},
		ReleaseName: name,
		Release:     release,
		Tag:         "-GRP",
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Title:         release.Title,
				OriginalTitle: release.Alt,
				Year:          release.Year,
			},
			TVDB: &api.TVDBMetadata{
				Name:        release.Alt,
				NameEnglish: release.Title,
				Year:        release.Year,
			},
		},
	}
	if category == "TV" {
		meta.SeasonInt = 1
		meta.SeasonStr = "S01"
		switch {
		case !containsNameElement(name, "S01"):
			meta.EpisodeInt = 2
			meta.EpisodeStr = "E02"
		case containsNameElement(name, "S01E02"):
			meta.EpisodeInt = 2
			meta.EpisodeStr = "E02"
		default:
			meta.TVPack = true
		}
	}
	meta.GeneratedReleaseNames.IncludeEpisodeTitle = api.ReleaseNameVariant{Name: name}
	return meta
}

func generatedCZSubject(category, name string, release api.ReleaseInfo, imdb *api.IMDBMetadata) api.UploadSubject {
	meta := generatedAZSubject(category, name, release)
	meta.ProviderMetadata.IMDB = imdb
	if azNoGroupPattern.MatchString(name) {
		meta.Tag = "-NOGRP"
	}
	if category == "TV" {
		meta.SeasonInt = 1
		meta.EpisodeInt = 2
		meta.SeasonStr = "S01"
		meta.EpisodeStr = "E02"
		meta.TVPack = false
	}
	return meta
}

func containsNameElement(name, element string) bool {
	_, ok := suffixAfterNameElement(name, element)
	return ok
}
