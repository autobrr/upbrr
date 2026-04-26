// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFilterDupesEmpty(t *testing.T) {
	t.Parallel()
	filtered, match := FilterDupes(nil, api.PreparedMetadata{}, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 0 {
		t.Fatalf("expected no filtered dupes")
	}
	if match.MatchedName != "" {
		t.Fatalf("expected empty match")
	}
}

func TestFilterDupesKeepsExactMatch(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{
		ReleaseName: "Movie.2024.1080p.WEBDL.x264-GRP",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
		SourcePath:  "x",
	}
	dupes := []api.DupeEntry{{Name: "Movie.2024.1080p.WEBDL.x264-GRP"}}
	filtered, _ := FilterDupes(dupes, meta, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 1 {
		t.Fatalf("expected one surviving dupe, got %d", len(filtered))
	}
}

func TestIsSeasonEpisodeMatchDailyEpisode(t *testing.T) {
	t.Parallel()

	matched, isSeasonPack := isSeasonEpisodeMatch("Show.2026.03.27.1080p.WEB-DL.x264-GRP", "", "2026-03-27")
	if !matched {
		t.Fatalf("expected daily episode to match")
	}
	if isSeasonPack {
		t.Fatalf("did not expect daily episode to be treated as season pack")
	}
}

func TestIsSeasonEpisodeMatchDailyEpisodeNonMatch(t *testing.T) {
	t.Parallel()

	matched, isSeasonPack := isSeasonEpisodeMatch("Show.2026.03.28.1080p.WEB-DL.x264-GRP", "", "2026-03-27")
	if matched {
		t.Fatalf("did not expect mismatched daily episode to match")
	}
	if isSeasonPack {
		t.Fatalf("did not expect mismatched daily episode to be treated as season pack")
	}
}

func TestFilterDupesOTWDropsSameSeasonEpisodeResolutionMismatch(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName: "Show.S1E02.1080p.WEB-DL.H264-GRP",
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		SeasonStr:   "S1",
		EpisodeStr:  "E02",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
		SourcePath:  "x",
	}
	dupes := []api.DupeEntry{
		{Name: "Show.S1E02.720p.WEB-DL.H264-OTHER"},
		{Name: "Show.S1E02.1080p.WEB-DL.H264-OTHER"},
	}

	filtered, _ := FilterDupes(dupes, meta, "OTW", config.Config{}, api.NopLogger{})
	if len(filtered) != 1 {
		t.Fatalf("expected one surviving dupe, got %d", len(filtered))
	}
	if got := filtered[0].Name; got != "Show.S1E02.1080p.WEB-DL.H264-OTHER" {
		t.Fatalf("unexpected surviving dupe %q", got)
	}
}

func TestOTWSameSeasonEpisodeResolutionMismatchIgnoresTVPacks(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName: "Show.S01.1080p.WEB-DL.H264-GRP",
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		SeasonInt:   1,
		TVPack:      true,
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
	}

	if isOTWSameSeasonEpisodeResolutionMismatch(meta, "OTW", "Show.S01E02.720p.WEB-DL.H264-OTHER", "S01", "E02", "1080p") {
		t.Fatalf("did not expect OTW same-season episode guard for TV packs")
	}
}

func TestOTWSameSeasonEpisodeResolutionMismatchMatchesAnyEpisodeInSeason(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		Release:     api.ReleaseInfo{Resolution: "1080p"},
	}

	if !isOTWSameSeasonEpisodeResolutionMismatch(meta, "OTW", "Show.S01E01.720p.WEB-DL.H264-OTHER", "S01", "E02", "1080p") {
		t.Fatalf("expected same-season episode with different resolution to be excluded")
	}
}
