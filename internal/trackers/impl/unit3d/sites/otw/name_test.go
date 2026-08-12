// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameUsesCurrentEnglishTMDBTVTitleAndDebutYear(t *testing.T) {
	t.Parallel()
	const sourcePath = "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP"
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 4,
			Category:   api.CanonicalCategoryTV,
			TMDBID:     987650001,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 4,
			TMDB: &api.TMDBMetadata{
				TMDBID:        987650001,
				Title:         "Example Show",
				OriginalTitle: "Example Série",
				Year:          2020,
			},
		},
		Release: api.ReleaseInfo{
			Title:      "Parsed Show",
			Alt:        "AKA Example Série",
			Resolution: "1080p",
		},
		SeasonInt:    1,
		EpisodeInt:   2,
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Example Episode",
		Edition:      "Limited Edition",
		Distributor:  "Criterion Collection",
		Service:      "DSNP",
		Type:         "WEBDL",
		Audio:        "DD+",
		Channels:     "5.1",
		VideoCodec:   "H.264",
		Tag:          "-GRP",
	}

	const want = "Example Show 2020 S01E02 Example Episode 1080p DSNP WEB-DL DD+ 5.1 H.264-GRP"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("OTW name = %q, want %q", got, want)
	} else if strings.Contains(got, "AKA") || strings.Contains(got, "Limited") || strings.Contains(got, "Criterion") {
		t.Fatalf("OTW non-disc name retained forbidden elements: %q", got)
	}
}

func TestBuildNameAllowsDistributorOnlyForCompleteDisc(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			TMDBID:   987650002,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 987650002,
				Title:  "Example Release",
				Year:   2026,
			},
		},
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "DISC",
		DiscType:    "BLURAY",
		Distributor: "Criterion Collection",
		Region:      "USA",
		Edition:     "Limited Edition",
		Source:      "Blu-ray",
		VideoCodec:  "AVC",
		Audio:       "DTS-HD MA",
		Channels:    "5.1",
	}
	got := buildName(meta, config.TrackerConfig{})
	if !strings.Contains(got, "1080p Criterion Collection USA") {
		t.Fatalf("OTW disc distributor missing: %q", got)
	}
	if strings.Contains(got, "Limited") {
		t.Fatalf("OTW disc retained edition: %q", got)
	}
}

func TestBuildNameBlocksProvedNonDiscMultiSeason(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			TMDBID:   987650003,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 987650003,
				Title:  "Example Show",
				Year:   2020,
			},
		},
		FileList: []string{
			filepath.Join("Example Show", "Season 01", "Example.Show.S01E01.mkv"),
			filepath.Join("Example Show", "Season 02", "Example.Show.S02E01.mkv"),
		},
		Type: "WEBDL",
	}
	if got := buildName(meta, config.TrackerConfig{}); got != "" {
		t.Fatalf("OTW non-disc multi-season name = %q, want blocked empty name", got)
	}
}

func TestProfileBuildNameVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().Site.BuildNameVersion; got != "v2" {
		t.Fatalf("OTW BuildNameVersion = %q, want v2", got)
	}
}
