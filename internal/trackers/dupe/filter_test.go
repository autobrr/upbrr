// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFilterDupesEmpty(t *testing.T) {
	t.Parallel()
	filtered, match := FilterDupes(nil, api.DuplicateSubject{}, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 0 {
		t.Fatalf("expected no filtered dupes")
	}
	if match.MatchedName != "" {
		t.Fatalf("expected empty match")
	}
}

func TestFilterDupesKeepsExactMatch(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{
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

func TestIsSeasonEpisodeMatchDoesNotCrossMatchUnpaddedSeason(t *testing.T) {
	t.Parallel()

	matched, isSeasonPack := isSeasonEpisodeMatch("Show.S10E02.1080p.WEB-DL.x264-GRP", "S1", "E02")
	if matched {
		t.Fatalf("did not expect S1 target to match S10 episode")
	}
	if isSeasonPack {
		t.Fatalf("did not expect S10E02 to be treated as season pack")
	}
}

func TestIsSeasonEpisodeMatchKeepsUnpaddedEpisodeAsEpisode(t *testing.T) {
	t.Parallel()

	matched, isSeasonPack := isSeasonEpisodeMatch("Show.S01E1.1080p.WEB-DL.x264-GRP", "S01", "E01")
	if !matched {
		t.Fatalf("expected unpadded episode to match padded target")
	}
	if isSeasonPack {
		t.Fatalf("did not expect unpadded episode to be treated as season pack")
	}
}

func TestIsSeasonEpisodeMatchAllowsUnpaddedSeasonEpisode(t *testing.T) {
	t.Parallel()

	matched, isSeasonPack := isSeasonEpisodeMatch("Show.S1E2.1080p.WEB-DL.x264-GRP", "S01", "E02")
	if !matched {
		t.Fatalf("expected unpadded season and episode to match padded target")
	}
	if isSeasonPack {
		t.Fatalf("did not expect unpadded season episode to be treated as season pack")
	}
}

func TestFilterDupesKeepsMatchingDailyEpisode(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName:      "Show.2026.03.27.1080p.WEB-DL.x264-GRP",
		Identity:         api.ExternalIdentity{Category: "TV"},
		DailyEpisodeDate: "2026-03-27",
		Release:          api.ReleaseInfo{Resolution: "1080p"},
		Type:             "WEBDL",
		SourcePath:       "x",
	}
	dupes := []api.DupeEntry{
		{Name: "Show.2026.03.27.1080p.WEB-DL.x264-OTHER"},
		{Name: "Show.2026.03.28.1080p.WEB-DL.x264-OTHER"},
	}

	filtered, _ := FilterDupes(dupes, meta, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 1 {
		t.Fatalf("expected one surviving dupe, got %d", len(filtered))
	}
	if got := filtered[0].Name; got != "Show.2026.03.27.1080p.WEB-DL.x264-OTHER" {
		t.Fatalf("unexpected surviving dupe %q", got)
	}
}

func TestFilterDupesSingleEpisodeKeepsMatchingSeasonPack(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName: "Show.S01E02.1080p.WEB-DL.x264-GRP",
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonStr:   "S01",
		EpisodeStr:  "E02",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
		SourcePath:  "x",
	}
	dupes := []api.DupeEntry{{
		Name: "Show.S01.1080p.WEB-DL.x264-OTHER",
		Link: "https://example.invalid/pack",
		ID:   "pack-1",
	}}

	filtered, match := FilterDupes(dupes, meta, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 1 {
		t.Fatalf("expected matching season pack to remain as dupe, got %d", len(filtered))
	}
	if !match.SeasonPackExists || !match.SeasonPackContainsEpisode {
		t.Fatalf("expected season pack match details, got %#v", match)
	}
	if match.SeasonPackID != "pack-1" || match.SeasonPackLink != "https://example.invalid/pack" {
		t.Fatalf("unexpected season pack match metadata: %#v", match)
	}
}

func TestFilterDupesTVPackExcludesSingleEpisodes(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName: "Show.S01.1080p.WEB-DL.x264-GRP",
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonStr:   "S01",
		TVPack:      true,
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
		SourcePath:  "x",
	}
	dupes := []api.DupeEntry{
		{Name: "Show.S01E02.1080p.WEB-DL.x264-OTHER"},
		{Name: "Show.S01.1080p.WEB-DL.x264-OTHER"},
	}

	filtered, _ := FilterDupes(dupes, meta, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 1 {
		t.Fatalf("expected only season pack dupe to remain, got %d", len(filtered))
	}
	if got := filtered[0].Name; got != "Show.S01.1080p.WEB-DL.x264-OTHER" {
		t.Fatalf("unexpected surviving dupe %q", got)
	}
}

func TestFilterDupesTVPackDoesNotCrossMatchSeason(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName: "Show.S01.1080p.WEB-DL.x264-GRP",
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonStr:   "S1",
		TVPack:      true,
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
		SourcePath:  "x",
	}
	dupes := []api.DupeEntry{{Name: "Show.S10.1080p.WEB-DL.x264-OTHER"}}

	filtered, _ := FilterDupes(dupes, meta, "AITHER", config.Config{}, api.NopLogger{})
	if len(filtered) != 0 {
		t.Fatalf("did not expect S1 pack to match S10 pack, got %#v", filtered)
	}
}

func TestFilterDupesOTWDropsSameSeasonEpisodeResolutionMismatch(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName: "Show.S1E02.1080p.WEB-DL.H264-GRP",
		Identity:    api.ExternalIdentity{Category: "TV"},
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

func TestSameSeasonEpisodeResolutionMismatchIgnoresTVPacks(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		ReleaseName: "Show.S01.1080p.WEB-DL.H264-GRP",
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonInt:   1,
		TVPack:      true,
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "WEBDL",
	}

	if isSameSeasonEpisodeResolutionMismatch(meta, "Show.S01E02.720p.WEB-DL.H264-OTHER", "S01", "E02", "1080p") {
		t.Fatalf("did not expect OTW same-season episode guard for TV packs")
	}
}

func TestSameSeasonEpisodeResolutionMismatchMatchesAnyEpisodeInSeason(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "TV"},
		Release:  api.ReleaseInfo{Resolution: "1080p"},
	}

	if !isSameSeasonEpisodeResolutionMismatch(meta, "Show.S01E01.720p.WEB-DL.H264-OTHER", "S01", "E02", "1080p") {
		t.Fatalf("expected same-season episode with different resolution to be excluded")
	}
}
