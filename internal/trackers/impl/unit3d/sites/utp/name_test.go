// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestBuildName verifies the from-scratch UTP naming reconstruction. Expected
// values are computed by hand-tracing the naming template for each type branch
// and category.
func TestBuildName(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "Movie ENCODE drops lossy audio, BDRip type tag, x264 encode",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				Source:      "BluRay",
				VideoCodec:  "H.264",
				VideoEncode: "x264",
				Audio:       "DD+ 5.1",
				HDR:         "",
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2020,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-GRP",
			},
			want: "Example Movie 2020 BDRip 1080p x264-GRP",
		},
		{
			name: "Movie REMUX keeps video_codec and lossless audio, UHD + HDR",
			meta: api.UploadSubject{
				Type:        "REMUX",
				Source:      "BluRay",
				VideoCodec:  "HEVC",
				VideoEncode: "x265",
				Audio:       "TrueHD Atmos 7.1",
				HDR:         "DV HDR10",
				UHD:         "UHD",
				Release: api.ReleaseInfo{
					Title:      "Example Film",
					Year:       2019,
					Resolution: "2160p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-TEAM",
			},
			want: "Example Film 2019 UHD BDRemux 2160p DV HDR10 HEVC TrueHD Atmos 7.1-TEAM",
		},
		{
			name: "Movie DISC keeps source, no type tag, lossless DTS-HD MA",
			meta: api.UploadSubject{
				Type:        "DISC",
				Source:      "Blu-ray",
				VideoCodec:  "HEVC",
				VideoEncode: "",
				Audio:       "DTS-HD MA 5.1",
				HDR:         "HDR10",
				UHD:         "UHD",
				Release: api.ReleaseInfo{
					Title:      "Example Feature",
					Year:       2015,
					Resolution: "2160p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-DISC",
			},
			want: "Example Feature 2015 UHD Blu-ray 2160p HDR10 HEVC DTS-HD MA 5.1-DISC",
		},
		{
			name: "Movie WEBDL with AKA, Hybrid, REPACK, Edition, Region, service as source",
			meta: api.UploadSubject{
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
				Release: api.ReleaseInfo{
					Title:      "Example Picture",
					Year:       2018,
					Resolution: "2160p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-X",
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{RetrievedAKA: "AKA Ejemplo Imagen"},
				},
			},
			want: "Example Picture AKA Ejemplo Imagen 2018 Hybrid REPACK Director's Cut EUR NF WEB-DL 2160p DV HEVC FLAC 2.0-X",
		},
		{
			name: "Movie WEBRIP strips Dual-Audio marker, keeps Atmos",
			meta: api.UploadSubject{
				Type:        "WEBRIP",
				Service:     "HULU",
				VideoEncode: "x265",
				Audio:       "Dual-Audio Atmos",
				Release: api.ReleaseInfo{
					Title:      "Example Indie",
					Year:       2020,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "",
			},
			want: "Example Indie 2020 HULU WEBRip 1080p x265 Atmos",
		},
		{
			name: "Movie without year drops the year slot",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				VideoEncode: "x264",
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       0,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-GRP",
			},
			want: "Example Movie BDRip 1080p x264-GRP",
		},
		{
			name: "TV WEBDL, season+episode before year, edition before repack order",
			meta: api.UploadSubject{
				Type:        "WEBDL",
				Service:     "AMZN",
				VideoEncode: "H.264",
				Audio:       "DDP5.1",
				SeasonStr:   "S02",
				EpisodeStr:  "E05",
				Release: api.ReleaseInfo{
					Title:      "Example Show",
					Year:       2021,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-GRP",
			},
			want: "Example Show S02E05 2021 AMZN WEB-DL 1080p H.264-GRP",
		},
		{
			name: "TV HDTV keeps source, video_encode codec",
			meta: api.UploadSubject{
				Type:        "HDTV",
				Source:      "HDTV",
				VideoEncode: "H.264",
				Audio:       "AAC",
				SeasonStr:   "S01",
				EpisodeStr:  "E10",
				Release: api.ReleaseInfo{
					Title:      "Example Program",
					Year:       2022,
					Resolution: "720p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-Z",
			},
			want: "Example Program S01E10 2022 HDTV 720p H.264-Z",
		},
		{
			name: "TV season pack (episode empty) keeps season only",
			meta: api.UploadSubject{
				Type:        "WEBDL",
				Service:     "DSNP",
				VideoEncode: "H.265",
				Audio:       "EAC3",
				SeasonStr:   "S03",
				EpisodeStr:  "",
				Release: api.ReleaseInfo{
					Title:      "Example Series",
					Year:       2023,
					Resolution: "2160p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-GRP",
			},
			want: "Example Series S03 2023 DSNP WEB-DL 2160p H.265-GRP",
		},
		{
			// The parsed title is the romaji the source directory was named with, so
			// the name must take the English title from the providers and carry the
			// romaji as the AKA.
			name: "TV REMUX anime uses provider English title with romaji AKA",
			meta: api.UploadSubject{
				Type:       "REMUX",
				Source:     "BluRay",
				VideoCodec: "AVC",
				Audio:      "Dual-Audio AAC 2.0",
				SeasonStr:  "S01",
				Release: api.ReleaseInfo{
					Title:      "Rei No Sakuhin",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-GRP",
				ProviderMetadata: api.SourceScopedMetadata{
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
			// A BluRay hybrid carries the marker in the edition rather than WebDV, and
			// the marker gets its own slot instead of being rendered as an edition.
			name: "TV REMUX hybrid renders the edition-carried marker in the hybrid slot",
			meta: api.UploadSubject{
				Type:       "REMUX",
				Source:     "BluRay",
				VideoCodec: "AVC",
				SeasonStr:  "S01",
				Edition:    "Hybrid",
				Release: api.ReleaseInfo{
					Title:      "Example Series",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-GRP",
			},
			want: "Example Series S01 2026 Hybrid BDRemux 1080p AVC-GRP",
		},
		{
			// The romaji equals the English title here, so TMDB retrieves no AKA at
			// all. The transliterations IMDb and the source name carry are not romaji
			// and must not stand in for one.
			name: "TV anime without a romaji AKA drops the transliterated original",
			meta: api.UploadSubject{
				Type:       "REMUX",
				Source:     "BluRay",
				VideoCodec: "AVC",
				Audio:      "Dual-Audio AAC 2.0",
				SeasonStr:  "S01",
				Release: api.ReleaseInfo{
					Title:      "Example Series",
					Alt:        "Egzâmpuru Shirîzu",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				Tag:      "-GRP",
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{
						Title:         "Example Series",
						OriginalTitle: "サンプル作品",
						Anime:         true,
					},
					IMDB: &api.IMDBMetadata{Title: "Example Series", AKA: "Egzâmpuru Shirîzu"},
				},
			},
			want: "Example Series S01 2026 BDRemux 1080p AVC-GRP",
		},
		{
			name: "TV drops non-Latin original title instead of putting it in the name",
			meta: api.UploadSubject{
				Type:       "REMUX",
				Source:     "BluRay",
				VideoCodec: "AVC",
				SeasonStr:  "S01",
				Release: api.ReleaseInfo{
					Title:      "Example Series",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "TV"},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Title: "Example Series", OriginalTitle: "サンプル作品"},
				},
			},
			want: "Example Series S01 2026 BDRemux 1080p AVC",
		},
		{
			name: "Movie drops Cyrillic original title instead of putting it in the name",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				VideoEncode: "x264",
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Tag:      "-GRP",
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Title: "Example Movie", OriginalTitle: "Приклад"},
				},
			},
			want: "Example Movie 2026 BDRip 1080p x264-GRP",
		},
		{
			name: "Unsupported category falls back to the generic base name",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				VideoEncode: "x264",
				ReleaseName: "Example.Release.2026.1080p.BluRay.x264-GRP",
				Release: api.ReleaseInfo{
					Title:      "Example Release",
					Year:       2026,
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{Category: "animation"},
				Tag:      "-GRP",
			},
			want: "Example.Release.2026.1080p.BluRay.x264-GRP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildName(tt.meta, config.TrackerConfig{})
			if got != tt.want {
				t.Fatalf("buildName()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestBuildNameInfersTypeWhenTypeFieldEmpty proves that buildName derives the
// release type from the same source as the type_id (unit3d.InferType) rather
// than the possibly-empty meta.Type. With meta.Type empty but REMUX inferable
// from the release name, the name uses the BDRemux type tag and agrees with the
// type_id (2 = REMUX).
func TestBuildNameInfersTypeWhenTypeFieldEmpty(t *testing.T) {
	meta := api.UploadSubject{
		Type:        "",
		Source:      "BluRay",
		VideoCodec:  "AVC",
		Audio:       "DTS-HD MA 5.1",
		ReleaseName: "Example.Film.2021.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
		Release: api.ReleaseInfo{
			Title:      "Example Film",
			Year:       2021,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Tag:      "-GRP",
	}
	if got := buildName(meta, config.TrackerConfig{}); got != "Example Film 2021 BDRemux 1080p AVC DTS-HD MA 5.1-GRP" {
		t.Fatalf("buildName() with empty Type: got %q", got)
	}
	if got := typeID(meta); got != "2" {
		t.Fatalf("expected type_id=2 (REMUX) to agree with inferred name, got %q", got)
	}
}

// TestBuildNameTitlePreferenceChain covers the English-title fallback order:
// TVDB English name for TV first, then TMDB, then IMDb, then the parsed title.
func TestBuildNameTitlePreferenceChain(t *testing.T) {
	base := api.UploadSubject{
		Type:        "WEBDL",
		Service:     "NF",
		VideoEncode: "H.264",
		SeasonStr:   "S01",
		Release: api.ReleaseInfo{
			Title:      "Parsed Series",
			Year:       2026,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{Category: "TV"},
		Tag:      "-GRP",
	}

	full := base
	full.ProviderMetadata = api.SourceScopedMetadata{
		TVDB: &api.TVDBMetadata{NameEnglish: "TVDB Series"},
		TMDB: &api.TMDBMetadata{Title: "TMDB Series"},
		IMDB: &api.IMDBMetadata{Title: "IMDB Series"},
	}
	if got := buildName(full, config.TrackerConfig{}); got != "TVDB Series S01 2026 NF WEB-DL 1080p H.264-GRP" {
		t.Fatalf("TVDB preference: got %q", got)
	}

	noTVDB := base
	noTVDB.ProviderMetadata = api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Title: "TMDB Series"},
		IMDB: &api.IMDBMetadata{Title: "IMDB Series"},
	}
	if got := buildName(noTVDB, config.TrackerConfig{}); got != "TMDB Series S01 2026 NF WEB-DL 1080p H.264-GRP" {
		t.Fatalf("TMDB preference: got %q", got)
	}

	imdbOnly := base
	imdbOnly.ProviderMetadata = api.SourceScopedMetadata{
		IMDB: &api.IMDBMetadata{Title: "IMDB Series"},
	}
	if got := buildName(imdbOnly, config.TrackerConfig{}); got != "IMDB Series S01 2026 NF WEB-DL 1080p H.264-GRP" {
		t.Fatalf("IMDB preference: got %q", got)
	}

	if got := buildName(base, config.TrackerConfig{}); got != "Parsed Series S01 2026 NF WEB-DL 1080p H.264-GRP" {
		t.Fatalf("parsed-title fallback: got %q", got)
	}
}

// TestBuildNameHonoursSuppressionOverrides covers the naming-only toggles. They
// never reach a metadata field, so a from-scratch builder like UTP has to read
// them off ReleaseNameOverrides or it silently ignores the user.
func TestBuildNameHonoursSuppressionOverrides(t *testing.T) {
	enabled := true
	meta := api.UploadSubject{
		Type:       "REMUX",
		Source:     "BluRay",
		VideoCodec: "AVC",
		SeasonStr:  "S01",
		Release: api.ReleaseInfo{
			Title:      "Rei No Sakuhin",
			Year:       2026,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{Category: "TV"},
		Tag:      "-GRP",
		ProviderMetadata: api.SourceScopedMetadata{
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
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("buildName()\n got: %q\nwant: %q", got, want)
	}
}
