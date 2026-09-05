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
			name: "CZ uses a country English AKA independent of production country",
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
							Title:    "Invalid Worldwide Title",
							Country:  "World-wide",
							Language: "English",
						},
						{
							Title:    "Example Film",
							Country:  "Otherland",
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
			name: "CZ prefers provider romanization to local transliteration",
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
			want: "Example Localized 2026 1080p WEB-DL H.265-GRP",
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

func TestCinemaZEnglishCountryAKA(t *testing.T) {
	tests := []struct {
		name                 string
		aka                  api.IMDBAKA
		originalUsesNonLatin bool
		want                 string
	}{
		{
			name: "country need not match production country",
			aka: api.IMDBAKA{
				Title:    "Example English Title",
				Country:  "Otherland",
				Language: "English",
			},
			want: "Example English Title",
		},
		{
			name: "worldwide is not country scoped",
			aka: api.IMDBAKA{
				Title:    "Example English Title",
				Country:  "World-wide",
				Language: "English",
			},
		},
		{
			name: "blank country is rejected",
			aka:  api.IMDBAKA{Title: "Example English Title", Language: "English"},
		},
		{
			name: "informal title is rejected",
			aka: api.IMDBAKA{
				Title:      "Example English Title",
				Country:    "Otherland",
				Language:   "English",
				Attributes: []string{"informal title"},
			},
		},
		{
			name: "working title is rejected",
			aka: api.IMDBAKA{
				Title:      "Example English Title",
				Country:    "Otherland",
				Language:   "English",
				Attributes: []string{"working title"},
			},
		},
		{
			name: "festival title is rejected",
			aka: api.IMDBAKA{
				Title:      "Example English Title",
				Country:    "Otherland",
				Language:   "English",
				Attributes: []string{"festival title"},
			},
		},
		{
			name: "transliterated Latin original is rejected",
			aka: api.IMDBAKA{
				Title:      "Example English Title",
				Country:    "Otherland",
				Language:   "English",
				Attributes: []string{"transliterated title"},
			},
		},
		{
			name: "transliterated non-Latin original is allowed",
			aka: api.IMDBAKA{
				Title:      "Example Romanization",
				Country:    "Otherland",
				Language:   "English",
				Attributes: []string{"transliterated title"},
			},
			originalUsesNonLatin: true,
			want:                 "Example Romanization",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := &api.IMDBMetadata{
				Country: "Exampleland",
				Akas:    []api.IMDBAKA{test.aka},
			}
			if got := cinemaZEnglishCountryAKA(metadata, test.originalUsesNonLatin); got != test.want {
				t.Fatalf("cinemaZEnglishCountryAKA() = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("non-Latin title does not hide later Latin AKA", func(t *testing.T) {
		metadata := &api.IMDBMetadata{Akas: []api.IMDBAKA{
			{
				Title:    "Пример фильма",
				Country:  "Otherland",
				Language: "English",
			},
			{
				Title:    "Example English Title",
				Country:  "Otherland",
				Language: "English",
			},
		}}
		if got, want := cinemaZEnglishCountryAKA(metadata, true), "Example English Title"; got != want {
			t.Fatalf("cinemaZEnglishCountryAKA() = %q, want %q", got, want)
		}
	})
}

func TestCinemaZNonLatinTitleFallbacks(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "provider romanization wins",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{Title: "Пример Фильм"},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Title: "Provider Romanization", AKA: "Пример Фильм"},
					TMDB: &api.TMDBMetadata{RetrievedAKA: "AKA Other Romanization", OriginalTitle: "Пример Фильм"},
				},
			},
			want: "Other Romanization",
		},
		{
			name: "supported local transliteration is last resort",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{Title: "Пример Фильм"},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Title: "Пример Фильм", AKA: "Пример Фильм"},
					TMDB: &api.TMDBMetadata{Title: "Пример Фильм", OriginalTitle: "Пример Фильм"},
				},
			},
			want: "Primer Film",
		},
		{
			name: "unsupported non-Latin title fails closed",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{Title: "例の作品"},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Title: "例の作品", AKA: "例の作品"},
					TMDB: &api.TMDBMetadata{Title: "例の作品", OriginalTitle: "例の作品"},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cinemaZTitle(test.meta); got != test.want {
				t.Fatalf("cinemaZTitle() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEditNameCinemaZFailsClosedOnlyForGeneratedNonLatinNames(t *testing.T) {
	generated := generatedCZSubject(
		"MOVIE",
		"例の作品 2026 1080p WEB-DL H.265-GRP",
		api.ReleaseInfo{
			Title: "例の作品",
			Alt:   "例の作品",
			Year:  2026,
		},
		&api.IMDBMetadata{Title: "例の作品", AKA: "例の作品"},
	)
	generated.ProviderMetadata.TMDB.Title = "例の作品"
	generated.ProviderMetadata.TMDB.OriginalTitle = "例の作品"
	if got := editName(siteFor("CZ"), generated); got != "" {
		t.Fatalf("generated non-Latin editName() = %q, want empty", got)
	}
	if _, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "CZ", Meta: generated},
		New("CZ").ReleaseNamePolicy(),
	); failure == nil || failure.Code() != "name_policy" {
		t.Fatalf("generated non-Latin policy failure = %v, want name_policy", failure)
	}

	requested := generated
	requested.ReleaseName = "Exact Requested 例の作品 2026-GRP"
	requested.GeneratedReleaseNames = api.GeneratedReleaseNameVariants{}
	if got := editName(siteFor("CZ"), requested); got != requested.ReleaseName {
		t.Fatalf("requested editName() = %q, want %q", got, requested.ReleaseName)
	}
}

func TestNormalizeCinemaZGeneratedName(t *testing.T) {
	tests := []struct {
		name        string
		category    string
		releaseName string
		configure   func(*api.UploadSubject)
		want        string
	}{
		{
			name: "edition rules and HYBRID placement",
			releaseName: "Example Film 2026 LIMITED Criterion Collection 25th Anniversary Edition Extended Cut Director's Cut 4K " +
				"REPACK Hybrid 2160p WEB-DL DD 5.1 H.265-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "WEBDL"
				meta.Release.Resolution = "2160p"
			},
			want: "Example Film 2026 EXT DC REPACK 2160p HYBRID WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name:        "BluRay remux orders HDR video and audio",
			releaseName: "Example Film 2026 2160p Hybrid UHD BluRay REMUX TrueHD 7.1 Atmos DV HDR10+ HEVC-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "REMUX"
				meta.DiscType = "BDMV"
				meta.Source = "BluRay"
				meta.Release.Resolution = "2160p"
				meta.UHD = "UHD"
				meta.HDR = "DV HDR10+"
				meta.VideoCodec = "HEVC"
				meta.Audio = "TrueHD 7.1 Atmos"
			},
			want: "Example Film 2026 2160p HYBRID UHD BluRay REMUX DV HDR10+ HEVC TrueHD 7.1 Atmos-GRP",
		},
		{
			name:        "Blu-ray raw adds the rip type and orders its technical tail",
			releaseName: "Example Film 2026 2160p USA UHD BluRay HDR HEVC DTS-HD MA 2.0-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "DISC"
				meta.DiscType = "BDMV"
				meta.Source = "BluRay"
				meta.Release.Resolution = "2160p"
				meta.Region = "USA"
				meta.UHD = "UHD"
				meta.HDR = "HDR"
				meta.VideoCodec = "HEVC"
				meta.Audio = "DTS-HD MA 2.0"
			},
			want: "Example Film 2026 2160p USA UHD Blu-ray RAW HDR HEVC DTS-HD MA 2.0-GRP",
		},
		{
			name:        "DVD raw adds resolution and video from metadata",
			releaseName: "Example Film 2026 R1 DVD DVD9 DD 5.1-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "DISC"
				meta.DiscType = "DVD"
				meta.Source = "DVD"
				meta.Release.Resolution = "480p"
				meta.Release.Size = "DVD9"
				meta.Region = "R1"
				meta.Audio = "DD 5.1"
				meta.VideoCodec = "MPEG-2"
			},
			want: "Example Film 2026 480p DVD9 DD 5.1 MPEG2-GRP",
		},
		{
			name:        "DVD remux wins over persisted disc type and adds omitted fields",
			releaseName: "Example Film 2026 DVD REMUX DD 2.0-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "REMUX"
				meta.DiscType = "DVD"
				meta.Source = "DVD"
				meta.Release.Resolution = "576p"
				meta.Audio = "DD 2.0"
				meta.VideoCodec = "MPEG-2"
			},
			want: "Example Film 2026 576p DVD Remux DD 2.0 MPEG2-GRP",
		},
		{
			name:        "DVDRip removes source and orders audio before video",
			releaseName: "Example Film 2026 DVD XviD DVDRip MP3-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "DVDRIP"
				meta.Source = "DVD"
				meta.Audio = "MP3"
				meta.VideoEncode = "XviD"
			},
			want: "Example Film 2026 DVDRip MP3 XviD-GRP",
		},
		{
			name:        "TV episode title words are not treated as release tags",
			category:    "TV",
			releaseName: "Example Film 2026 S01E02 Limited Hybrid Extended Cut Hybrid 1080p WEB-DL DD 5.1 H.265-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "WEBDL"
				meta.EpisodeTitle = "Parsed Episode"
				meta.ProviderMetadata.TVDB.EpisodeNameEnglish = "Limited Hybrid"
				meta.Release.Resolution = "1080p"
			},
			want: "Example Film 2026 S01E02 Limited Hybrid EXT 1080p HYBRID WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name:        "manual TV episode title remains protected when variants match",
			category:    "TV",
			releaseName: "Example Film 2026 S01E02 Limited Hybrid 1080p WEB-DL DD 5.1 H.265-GRP",
			configure: func(meta *api.UploadSubject) {
				meta.Type = "WEBDL"
				meta.EpisodeTitle = "Limited Hybrid"
				meta.GeneratedReleaseNames.OmitEpisodeTitle = meta.GeneratedReleaseNames.IncludeEpisodeTitle
				meta.Release.Resolution = "1080p"
			},
			want: "Example Film 2026 S01E02 Limited Hybrid 1080p WEB-DL DD 5.1 H.265-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			category := test.category
			if category == "" {
				category = "MOVIE"
			}
			meta := generatedCZSubject(
				category,
				test.releaseName,
				api.ReleaseInfo{
					Title: "Example Localized",
					Alt:   "Example Original",
					Year:  2026,
				},
				&api.IMDBMetadata{
					AKA: "Example Original",
					Akas: []api.IMDBAKA{{
						Title:    "Example Film",
						Country:  "Otherland",
						Language: "English",
					}},
				},
			)
			test.configure(&meta)
			if got := editName(siteFor("CZ"), meta); got != test.want {
				t.Fatalf("editName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeCinemaZGeneratedNameDoesNotEditTitleWords(t *testing.T) {
	meta := api.UploadSubject{
		DiscType:   "BDMV",
		Source:     "BluRay",
		UHD:        "UHD",
		HDR:        "HDR",
		Region:     "USA",
		VideoCodec: "HEVC",
		Audio:      "DTS-HD MA 2.0",
		Tag:        "-GRP",
		Release:    api.ReleaseInfo{Resolution: "2160p"},
	}
	name := "Example HDR 2026 2160p USA UHD BluRay HDR HEVC DTS-HD MA 2.0-GRP"
	want := "Example HDR 2026 2160p USA UHD Blu-ray RAW HDR HEVC DTS-HD MA 2.0-GRP"
	if got := normalizeCinemaZGeneratedName(meta, "Example HDR", name); got != want {
		t.Fatalf("normalizeCinemaZGeneratedName() = %q, want %q", got, want)
	}
}

func TestAZFamilyNamingPolicyVersions(t *testing.T) {
	tests := []struct {
		site         string
		want         string
		yearProvider api.IdentityProvider
	}{
		{
			site:         "AZ",
			want:         "azfamily/az/v2",
			yearProvider: api.IdentityProviderTMDB,
		},
		{
			site:         "CZ",
			want:         "azfamily/cz/v3",
			yearProvider: api.IdentityProviderIMDB,
		},
		{
			site:         "PHD",
			want:         "azfamily/phd/v2",
			yearProvider: api.IdentityProviderTMDB,
		},
	}
	for _, test := range tests {
		t.Run(test.site, func(t *testing.T) {
			policy := New(test.site).ReleaseNamePolicy()
			if got := policy.ID; got != test.want {
				t.Fatalf("policy ID = %q, want %q", got, test.want)
			}
			if got := policy.MovieYearProvider; got != test.yearProvider {
				t.Fatalf("movie year provider = %q, want %q", got, test.yearProvider)
			}
		})
	}
}

func TestCinemaZReleaseNamePolicyUsesIMDbProductionYear(t *testing.T) {
	meta := generatedCZSubject(
		"MOVIE",
		"Example Localized Example Original 2025 1080p WEB-DL H.265-GRP",
		api.ReleaseInfo{
			Title: "Example Localized",
			Alt:   "Example Original",
			Year:  2025,
		},
		&api.IMDBMetadata{
			Title: "Example Film",
			AKA:   "Example Original",
			Year:  2024,
			Akas: []api.IMDBAKA{{
				Title:    "Example Film",
				Country:  "Otherland",
				Language: "English",
			}},
		},
	)
	meta.ProviderMetadata.TMDB.Year = 2026

	input, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "CZ", Meta: meta},
		New("CZ").ReleaseNamePolicy(),
	)
	if failure != nil {
		t.Fatalf("resolve CinemaZ release name: %v", failure)
	}
	if got, want := input.Projection.UploadReleaseName, "Example Film 2024 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("CinemaZ upload name = %q, want %q", got, want)
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
