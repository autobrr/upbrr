// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"testing"
)

func TestParseReleaseInfoPreservesHybridOther(t *testing.T) {
	release := ParseReleaseInfo("Example.Release.2026.2160p.WEB-DL.HYBRiD.DDP5.1.Atmos.DV.H.265-GRP.mkv")
	if !slices.Equal(release.Other, []string{"HYBRiD"}) {
		t.Fatalf("expected exact Hybrid parser output, got %#v", release.Other)
	}
}

func TestParseReleaseInfoCollectsFinalTechnicalMarkers(t *testing.T) {
	t.Parallel()

	release := ParseReleaseInfo("Example.Release.2026.2160p.BluRay.FANRES.Regraded.Incomplete.UPSCL.UPSUHD.MIC.x265.v2-GRP.mkv")
	if !slices.Equal(release.Other, []string{"FANRES", "Regraded", "Incomplete", "UPSCL", "UPSUHD", "MIC"}) {
		t.Fatalf("other = %#v", release.Other)
	}
	if release.Version != "v2" {
		t.Fatalf("version = %q, want v2", release.Version)
	}
}

func TestParseReleaseInfoOmitsMarkersOutsideTechnicalPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "title",
			input: "FANRES.Regraded.Incomplete.UPSCL.UPSUHD.MIC.2026.2160p.BluRay.x265-GRP.mkv",
		},
		{
			name:  "group",
			input: "Example.Release.2026.2160p.BluRay.x265-FANRES.mkv",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			release := ParseReleaseInfo(tc.input)
			if len(release.Other) != 0 {
				t.Fatalf("other = %#v, want none", release.Other)
			}
		})
	}
}

func TestParseReleaseInfoPreservesDefaultParserFacts(t *testing.T) {
	t.Parallel()

	release := ParseReleaseInfo("Example.Release.2026.2160p.BluRay.HYBRiD.DDP5.1.H.265-GRP.mkv")
	if release.Title != "Example Release" || release.Resolution != "2160p" || release.Group != "GRP" ||
		!slices.Equal(release.Audio, []string{"DDP"}) || !slices.Equal(release.Other, []string{"HYBRiD"}) {
		t.Fatalf("release = %#v", release)
	}
}

func TestParseReleaseInfoCollectsAIRemasterMarker(t *testing.T) {
	t.Parallel()

	release := ParseReleaseInfo("Example.Release.2026.2160p.BluRay.AI.Remaster.x265-GRP.mkv")
	if !slices.Contains(release.Other, "AI Remaster") {
		t.Fatalf("other = %#v, want AI Remaster", release.Other)
	}
}

func TestParseReleaseInfo(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		category string
		typ      string
		source   string
		site     string
		group    string
	}{
		{
			name:     "empty input returns defaults",
			input:    "",
			category: "",
			typ:      "",
			source:   "",
		},
		{
			name:     "malformed filename leaves derived fields empty",
			input:    "invalid_filename",
			category: "",
			typ:      "",
			source:   "",
		},
		{
			name:     "movie uses rls category and source",
			input:    "Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBDL",
			source:   "Web",
			group:    "GRP",
		},
		{
			name:     "episode uses tv category and webdl source",
			input:    "Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
			category: "TV",
			typ:      "WEBDL",
			source:   "Web",
			group:    "GRP",
		},
		{
			name:     "season pack uses tv category",
			input:    "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP",
			category: "TV",
			typ:      "WEBDL",
			source:   "Web",
			group:    "GRP",
		},
		{
			name:     "bare web filename uses webdl before webrip",
			input:    "Movie.2026.2160p.WEB.DDP5.1.H.265-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBDL",
			source:   "Web",
			group:    "GRP",
		},
		{
			name:     "glued bdremux resolves remux and bluray",
			input:    "Show S01 2019 BDRemux 1080p",
			category: "TV",
			typ:      "REMUX",
			source:   "BluRay",
		},
		{
			name:     "glued bdrip resolves encode",
			input:    "Show S01 2022 BDRip 1080p-GRP",
			category: "TV",
			typ:      "ENCODE",
			source:   "BDRiP",
			group:    "GRP",
		},
		{
			name:     "compact bdmv infers bluray source",
			input:    "Example.Show.S01.2026.BDMV.1080p",
			category: "TV",
			source:   "BluRay",
		},
		{
			name:     "webrip filename uses webrip",
			input:    "Movie.2026.2160p.WEBRip.DDP5.1.H.265-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBRIP",
			source:   "Web",
			group:    "GRP",
		},
		{
			name:     "bluray remux preserves distinct source and type",
			input:    "Movie.2026.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv",
			category: "MOVIE",
			typ:      "REMUX",
			source:   "BluRay",
			group:    "GRP",
		},
		{
			name:     "bluray encode infers encode type",
			input:    "Movie.2026.1080p.BluRay.x264-GRP.mkv",
			category: "MOVIE",
			typ:      "ENCODE",
			source:   "BluRay",
			group:    "GRP",
		},
		{
			name:     "leading bracket anime group falls back from site",
			input:    "[SubsPlease] Re Zero kara Hajimeru Isekai Seikatsu - 77 (1080p) [F7DAEC64].mkv",
			category: "TV",
			site:     "SubsPlease",
			group:    "SubsPlease",
		},
		{
			name:     "explicit release group wins over leading bracket site",
			input:    "[SubsPlease] Show.S01E02.1080p.WEB-DL.x264-GRP.mkv",
			category: "TV",
			typ:      "WEBDL",
			source:   "Web",
			site:     "SubsPlease",
			group:    "GRP",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			release := ParseReleaseInfo(tc.input)

			if release.Category != tc.category {
				t.Errorf("expected category %q, got %q", tc.category, release.Category)
			}
			if release.Type != tc.typ {
				t.Errorf("expected type %q, got %q", tc.typ, release.Type)
			}
			if release.Source != tc.source {
				t.Errorf("expected source %q, got %q", tc.source, release.Source)
			}
			if release.Site != tc.site {
				t.Errorf("expected site %q, got %q", tc.site, release.Site)
			}
			if release.Group != tc.group {
				t.Errorf("expected group %q, got %q", tc.group, release.Group)
			}
		})
	}
}

func TestParseReleaseInfoSeasonTokenWidths(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"768x576", "1920x800", "1x05", "01x05", "2026x03", "S1E03", "S123E03", "S12345E03", "S1", "S123", "Season 1", "Series 123", "S1E03E04", "S123E03E04"} {
		t.Run(token, func(t *testing.T) {
			got := ParseReleaseInfo("Example.Show." + token + ".1080p.WEB-DL.mkv")
			if got.Season != 0 || got.Episode != 0 || got.Category == "TV" {
				t.Fatalf("rejected token %q produced season=%d episode=%d category=%s", token, got.Season, got.Episode, got.Category)
			}
		})
	}
	for _, tc := range []struct {
		input           string
		season, episode int
	}{
		{"S01E03", 1, 3}, {"S2026E03", 2026, 3}, {"S01", 1, 0}, {"S2026", 2026, 0}, {"S01E03E04", 1, 3}, {"S2026E03E04", 2026, 3},
		{"Season.01.Episode.03", 1, 3}, {"S01S02", 1, 0}, {"S01.Disc02", 1, 0}, {"S00E03", 0, 3}, {"E03", 0, 3},
		{"1x05.S02E04", 2, 4}, {"S1E03.S02E04", 2, 4}, {"768x576.S2026E03", 2026, 3},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseReleaseInfo("Example.Show." + tc.input + ".1080p.WEB-DL.mkv")
			if got.Season != tc.season || got.Episode != tc.episode {
				t.Fatalf("valid token %q produced season=%d episode=%d; want %d,%d", tc.input, got.Season, got.Episode, tc.season, tc.episode)
			}
		})
	}
}

func TestParseReleaseInfoPreservesDailyAndAbsoluteEpisodes(t *testing.T) {
	t.Parallel()
	daily := ParseReleaseInfo("Example.Show.2026.03.04.1080p.WEB-DL.mkv")
	if daily.Category != "TV" || daily.Year != 2026 || daily.Month != 3 || daily.Day != 4 {
		t.Fatalf("daily date changed: %+v", daily)
	}
	anime := ParseReleaseInfo("[GRP] Example Anime - 123 (1080p).mkv")
	if anime.Episode != 123 || anime.Category != "TV" {
		t.Fatalf("absolute episode changed: %+v", anime)
	}
	release := ParseReleaseInfo("Example.Movie.2026.1920x800.1080p.WEB-DL.x264-GRP.mkv")
	if release.Year != 2026 || release.Resolution != "1080p" || release.Source != "Web" || release.Group != "GRP" {
		t.Fatalf("technical fields changed: %+v", release)
	}
}
