// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestBuildUTPName verifies the from-scratch UTOPIA naming reconstruction.
// Expected values are computed by hand-tracing the naming template for each type
// branch and category.
func TestBuildUTPName(t *testing.T) {
	tests := []struct {
		name string
		meta api.PreparedMetadata
		want string
	}{
		{
			name: "Movie ENCODE drops lossy audio, BDRip type tag, x264 encode",
			meta: api.PreparedMetadata{
				Type:        "ENCODE",
				Source:      "BluRay",
				VideoCodec:  "H.264",
				VideoEncode: "x264",
				Audio:       "DD+ 5.1",
				HDR:         "",
				Release:     api.ReleaseInfo{Title: "The Movie", Year: 2020, Resolution: "1080p"},
				Tag:         "-GRP",
			},
			want: "The Movie 2020 BDRip 1080p x264-GRP",
		},
		{
			name: "Movie REMUX keeps video_codec and lossless audio, UHD + HDR",
			meta: api.PreparedMetadata{
				Type:        "REMUX",
				Source:      "BluRay",
				VideoCodec:  "HEVC",
				VideoEncode: "x265",
				Audio:       "TrueHD Atmos 7.1",
				HDR:         "DV HDR10",
				UHD:         "UHD",
				Release:     api.ReleaseInfo{Title: "Some Film", Year: 2019, Resolution: "2160p"},
				Tag:         "-TEAM",
			},
			want: "Some Film 2019 UHD BDRemux 2160p DV HDR10 HEVC TrueHD Atmos 7.1-TEAM",
		},
		{
			name: "Movie DISC keeps source, no type tag, lossless DTS-HD MA",
			meta: api.PreparedMetadata{
				Type:        "DISC",
				Source:      "Blu-ray",
				VideoCodec:  "HEVC",
				VideoEncode: "",
				Audio:       "DTS-HD MA 5.1",
				HDR:         "HDR10",
				UHD:         "UHD",
				Release:     api.ReleaseInfo{Title: "Big Movie", Year: 2015, Resolution: "2160p"},
				Tag:         "-DISC",
			},
			want: "Big Movie 2015 UHD Blu-ray 2160p HDR10 HEVC DTS-HD MA 5.1-DISC",
		},
		{
			name: "Movie WEBDL with AKA, Hybrid, REPACK, Edition, Region, service as source",
			meta: api.PreparedMetadata{
				Type:        "WEBDL",
				Service:     "NF",
				VideoCodec:  "HEVC",
				VideoEncode: "HEVC",
				Audio:       "FLAC 2.0",
				HDR:         "DV",
				Edition:     "Director's Cut",
				Repack:      "REPACK",
				Region:      "EUR",
				WebDV:       true,
				Release:     api.ReleaseInfo{Title: "Film", Year: 2018, Resolution: "2160p"},
				Tag:         "-X",
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{RetrievedAKA: "AKA Filme"},
				},
			},
			want: "Film AKA Filme 2018 Hybrid REPACK Director's Cut EUR NF WEB-DL 2160p DV HEVC FLAC 2.0-X",
		},
		{
			name: "Movie WEBRIP strips Dual-Audio marker, keeps Atmos",
			meta: api.PreparedMetadata{
				Type:        "WEBRIP",
				Service:     "HULU",
				VideoEncode: "x265",
				Audio:       "Dual-Audio Atmos",
				Release:     api.ReleaseInfo{Title: "Indie", Year: 2020, Resolution: "1080p"},
				Tag:         "",
			},
			want: "Indie 2020 HULU WEBRip 1080p x265 Atmos",
		},
		{
			name: "TV WEBDL, season+episode before year, edition before repack order",
			meta: api.PreparedMetadata{
				Type:        "WEBDL",
				Service:     "AMZN",
				VideoEncode: "H.264",
				Audio:       "DDP5.1",
				SeasonStr:   "S02",
				EpisodeStr:  "E05",
				Release:     api.ReleaseInfo{Title: "The Show", Year: 2021, Resolution: "1080p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "-GRP",
			},
			want: "The Show S02E05 2021 AMZN WEB-DL 1080p H.264-GRP",
		},
		{
			name: "TV HDTV keeps source, video_encode codec",
			meta: api.PreparedMetadata{
				Type:        "HDTV",
				Source:      "HDTV",
				VideoEncode: "H.264",
				Audio:       "AAC",
				SeasonStr:   "S01",
				EpisodeStr:  "E10",
				Release:     api.ReleaseInfo{Title: "News", Year: 2022, Resolution: "720p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "-Z",
			},
			want: "News S01E10 2022 HDTV 720p H.264-Z",
		},
		{
			name: "TV season pack (episode empty) keeps season only",
			meta: api.PreparedMetadata{
				Type:        "WEBDL",
				Service:     "DSNP",
				VideoEncode: "H.265",
				Audio:       "EAC3",
				SeasonStr:   "S03",
				EpisodeStr:  "",
				Release:     api.ReleaseInfo{Title: "Series", Year: 2023, Resolution: "2160p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "-GRP",
			},
			want: "Series S03 2023 DSNP WEB-DL 2160p H.265-GRP",
		},
		{
			// The parsed title is the romaji the source directory was named with, so
			// the name must take the English title from the providers and carry the
			// romaji as the AKA.
			name: "TV REMUX anime uses provider English title with romaji AKA",
			meta: api.PreparedMetadata{
				Type:        "REMUX",
				Source:      "BluRay",
				VideoCodec:  "AVC",
				Audio:       "Dual-Audio AAC 2.0",
				SeasonStr:   "S01",
				Release:     api.ReleaseInfo{Title: "Rei No Sakuhin", Year: 2026, Resolution: "1080p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "-GRP",
				ExternalMetadata: api.ExternalMetadata{
					TVDB: &api.TVDBMetadata{Name: "サンプル作品", NameEnglish: "Example Anime Series"},
					TMDB: &api.TMDBMetadata{
						Title:         "Example Anime Series",
						OriginalTitle: "サンプル作品",
						RetrievedAKA:  "AKA Rei No Sakuhin",
					},
				},
			},
			want: "Example Anime Series AKA Rei No Sakuhin S01 2026 BDRemux 1080p AVC-GRP",
		},
		{
			// The romaji equals the English title here, so TMDB retrieves no AKA at all.
			// The transliterations IMDb and the source name carry are not romaji and must
			// not stand in for one.
			name: "TV anime without a romaji AKA drops the transliterated original",
			meta: api.PreparedMetadata{
				Type:        "REMUX",
				Source:      "BluRay",
				VideoCodec:  "AVC",
				Audio:       "Dual-Audio AAC 2.0",
				SeasonStr:   "S01",
				Release:     api.ReleaseInfo{Title: "Example Series", Alt: "Egzâmpuru Shirîzu", Year: 2026, Resolution: "1080p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				Tag:         "-GRP",
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{Title: "Example Series", OriginalTitle: "サンプル作品", Anime: true},
					IMDB: &api.IMDBMetadata{Title: "Example Series", AKA: "Egzâmpuru Shirîzu"},
				},
			},
			want: "Example Series S01 2026 BDRemux 1080p AVC-GRP",
		},
		{
			name: "TV drops non-Latin original title instead of putting it in the name",
			meta: api.PreparedMetadata{
				Type:        "REMUX",
				Source:      "BluRay",
				VideoCodec:  "AVC",
				SeasonStr:   "S01",
				Release:     api.ReleaseInfo{Title: "Example Series", Year: 2026, Resolution: "1080p"},
				ExternalIDs: api.ExternalIDs{Category: "TV"},
				ExternalMetadata: api.ExternalMetadata{
					TMDB: &api.TMDBMetadata{Title: "Example Series", OriginalTitle: "サンプル作品"},
				},
			},
			want: "Example Series S01 2026 BDRemux 1080p AVC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildUTPName(tt.meta)
			if got != tt.want {
				t.Fatalf("buildUTPName()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestBuildUTPNameInfersTypeWhenTypeFieldEmpty proves that buildUTPName derives
// the release type from the same source as the type_id (inferUnit3DType) rather
// than the possibly-empty meta.Type. With meta.Type empty but REMUX inferable
// from the release name, the name uses the BDRemux type tag and agrees with the
// type_id (2 = REMUX). Before this fix the name wrongly kept the raw source tag.
func TestBuildUTPNameInfersTypeWhenTypeFieldEmpty(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:        "",
		Source:      "BluRay",
		VideoCodec:  "AVC",
		Audio:       "DTS-HD MA 5.1",
		ReleaseName: "The.Film.2021.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
		Release:     api.ReleaseInfo{Title: "The Film", Year: 2021, Resolution: "1080p"},
		Tag:         "-GRP",
	}
	if got := buildUTPName(meta); got != "The Film 2021 BDRemux 1080p AVC DTS-HD MA 5.1-GRP" {
		t.Fatalf("buildUTPName() with empty Type: got %q", got)
	}
	if got := resolveUnit3DUTPTypeID(meta); got != "2" {
		t.Fatalf("expected type_id=2 (REMUX) to agree with inferred name, got %q", got)
	}
}

// TestBuildUTPNameRoutedThroughBuildUnit3DName confirms the tracker dispatch in
// buildUnit3DName reaches the UTP builder.
func TestBuildUTPNameRoutedThroughBuildUnit3DName(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:        "ENCODE",
		VideoEncode: "x264",
		Audio:       "DD+ 5.1",
		Release:     api.ReleaseInfo{Title: "The Movie", Year: 2020, Resolution: "1080p"},
		Tag:         "-GRP",
	}
	if got := buildUnit3DName("UTP", meta, config.TrackerConfig{}); got != "The Movie 2020 BDRip 1080p x264-GRP" {
		t.Fatalf("routing to buildUTPName failed, got %q", got)
	}
}

func TestUTPCategoryIDViaDefault(t *testing.T) {
	movie := api.PreparedMetadata{Release: api.ReleaseInfo{Title: "Film", Year: 2020}}
	if got := resolveUnit3DCategoryIDForTracker("UTP", movie); got != "1" {
		t.Fatalf("expected movie category_id=1, got %q", got)
	}
	tv := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}}
	if got := resolveUnit3DCategoryIDForTracker("UTP", tv); got != "2" {
		t.Fatalf("expected TV category_id=2, got %q", got)
	}
}

func TestResolveUnit3DUTPTypeID(t *testing.T) {
	cases := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}
	for typeValue, want := range cases {
		meta := api.PreparedMetadata{Type: typeValue}
		if got := resolveUnit3DUTPTypeID(meta); got != want {
			t.Fatalf("type %q: expected %q, got %q", typeValue, want, got)
		}
	}
	// Unknown type falls back to ENCODE (3).
	if got := resolveUnit3DUTPTypeID(api.PreparedMetadata{Type: "MYSTERY"}); got != "3" {
		t.Fatalf("expected unknown type fallback=3, got %q", got)
	}
}

func TestSwapUTPImageURLs(t *testing.T) {
	images := []api.ScreenshotImage{
		{ImgURL: "https://host/medium.png", RawURL: "https://host/full.png", WebURL: "https://host/page"},
		{ImgURL: "", RawURL: "https://host/full2.png", WebURL: "https://host/page2"},
	}
	got := swapUTPImageURLs(images)

	// First image: full moves to WebURL (link), medium moves to RawURL (display).
	if got[0].WebURL != "https://host/full.png" || got[0].RawURL != "https://host/medium.png" {
		t.Fatalf("expected swapped URLs, got web=%q raw=%q", got[0].WebURL, got[0].RawURL)
	}
	// Second image lacks a medium thumbnail and is left unchanged.
	if got[1].WebURL != "https://host/page2" || got[1].RawURL != "https://host/full2.png" {
		t.Fatalf("expected unchanged URLs, got web=%q raw=%q", got[1].WebURL, got[1].RawURL)
	}
	// Input slice must not be mutated.
	if images[0].WebURL != "https://host/page" {
		t.Fatalf("input slice mutated: %q", images[0].WebURL)
	}
}

// TestBuildUTPNameHonoursSuppressionOverrides covers the naming-only toggles.
// They never reach a metadata field, so a from-scratch builder like UTP has to
// read them off ReleaseNameOverrides or it silently ignores the user.
func TestBuildUTPNameHonoursSuppressionOverrides(t *testing.T) {
	enabled := true
	meta := api.PreparedMetadata{
		Type:        "REMUX",
		Source:      "BluRay",
		VideoCodec:  "AVC",
		SeasonStr:   "S01",
		Release:     api.ReleaseInfo{Title: "Rei No Sakuhin", Year: 2026, Resolution: "1080p"},
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		Tag:         "-GRP",
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				Title:        "Example Anime Series",
				RetrievedAKA: "AKA Rei No Sakuhin",
			},
		},
	}
	meta.ReleaseNameOverrides = api.ReleaseNameOverrides{
		NoYear:   &enabled,
		NoSeason: &enabled,
		NoAKA:    &enabled,
	}

	want := "Example Anime Series BDRemux 1080p AVC-GRP"
	if got := buildUTPName(meta); got != want {
		t.Fatalf("buildUTPName()\n got: %q\nwant: %q", got, want)
	}
}
