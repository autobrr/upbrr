// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildReleaseNameMovieWebDL(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example Movie",
		Year:        2026,
		Resolution:  "2160p",
		Service:     "NF",
		Audio:       "DD+5.1",
		HDR:         "HDR",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	}, api.NopLogger{})

	expectedName := "Example Movie 2026 2160p NF WEB-DL DD+5.1 HDR H.265-GRP"
	if result.Name != expectedName {
		t.Fatalf("expected name %q, got %q", expectedName, result.Name)
	}
	if result.NameNoTag != "Example Movie 2026 2160p NF WEB-DL DD+5.1 HDR H.265" {
		t.Fatalf("expected name without tag, got %q", result.NameNoTag)
	}
	if result.CleanName == "" {
		t.Fatalf("expected clean name")
	}
	if len(result.MissingFields) != 2 {
		t.Fatalf("expected missing fields, got %v", result.MissingFields)
	}
}

func TestBuildReleaseNameMovieBareWebEncodeUsesWebDLNaming(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example Movie 2: Migration",
		Year:        2026,
		Resolution:  "2160p",
		Service:     "iT",
		Source:      "WEB",
		Audio:       "DD+ Atmos 5.1",
		HDR:         "HDR10+",
		VideoEncode: "x265",
		Tag:         "-GRP",
	}, api.NopLogger{})

	expectedName := "Example Movie 2: Migration 2026 2160p iT WEB-DL DD+ 5.1 Atmos HDR10+ x265-GRP"
	if result.Name != expectedName {
		t.Fatalf("expected name %q, got %q", expectedName, result.Name)
	}
	if strings.Contains(result.NameNoTag, "UHD") {
		t.Fatalf("expected no UHD in WEB-DL name, got %q", result.NameNoTag)
	}
}

func TestBuildReleaseNameHybridEdition(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Hybrid Cut",
		Year:        2020,
		Edition:     "Hybrid Director",
		WebDV:       true,
		Resolution:  "1080p",
		Source:      "BluRay",
		Audio:       "DTS",
		VideoEncode: "x264",
	}, api.NopLogger{})

	expected := "Hybrid Cut 2020 Director Hybrid 1080p BluRay DTS x264"
	if result.NameNoTag != expected {
		t.Fatalf("expected %q, got %q", expected, result.NameNoTag)
	}
}

func TestBuildReleaseNameEncodeFallsBackToVideoCodec(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "ENCODE",
		Title:      "Example Animation",
		Year:       2026,
		Resolution: "1080p",
		Audio:      "VORBIS 2.0",
		VideoCodec: "VP8",
	}, api.NopLogger{})

	expected := "Example Animation 2026 1080p VORBIS 2.0 VP8"
	if result.NameNoTag != expected {
		t.Fatalf("expected %q, got %q", expected, result.NameNoTag)
	}
}

func TestBuildReleaseNameCleanName(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category: "MOVIE",
		Type:     "HDTV",
		Title:    "Bad:Name",
		Year:     2001,
		Source:   "HDTV",
		Audio:    "AAC",
	}, api.NopLogger{})

	if result.CleanName == "" {
		t.Fatalf("expected clean name")
	}
	if result.CleanName == result.Name {
		t.Fatalf("expected clean name to sanitize invalid characters")
	}
}

func TestApplyReleaseNameOverrides(t *testing.T) {
	base := api.ReleaseNameRequest{
		Category: "MOVIE",
		Title:    "Example",
		Tag:      "-GROUP",
		Audio:    "DD+5.1",
		Edition:  "Director",
	}
	overrides := api.ReleaseNameOverrides{
		NoTag:      new(true),
		ManualYear: new(2025),
		ManualDate: new("2025-01-01"),
		NoAKA:      new(true),
	}
	updated := applyReleaseNameOverrides(base, overrides, api.NopLogger{})
	if updated.Tag != "" {
		t.Fatalf("expected tag cleared, got %q", updated.Tag)
	}
	if updated.ManualYear != 2025 {
		t.Fatalf("expected manual year, got %d", updated.ManualYear)
	}
	if !updated.ManualDate || updated.DailyDate != "2025-01-01" {
		t.Fatalf("expected manual date applied, got manual=%t date=%q", updated.ManualDate, updated.DailyDate)
	}
	if !updated.NoAKA {
		t.Fatalf("expected no aka override")
	}
}

func TestReleaseNameRequestFromMetaDefaultsToDailyForTVEpisode(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:      api.ExternalIDs{Category: "TV"},
		Release:          api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		Type:             "WEBDL",
		Source:           "Web",
		Service:          "AMZN",
		Audio:            "AAC 2.0",
		VideoEncode:      "H.264",
		DailyEpisodeDate: "2025-11-10",
		EpisodeTitle:     "Episode Title",
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if !req.ManualDate {
		t.Fatalf("expected daily naming to be enabled by default")
	}
	if req.DailyDate != "2025-11-10" {
		t.Fatalf("expected daily date in request, got %q", req.DailyDate)
	}
	if req.EpisodeTitle != "Episode Title" {
		t.Fatalf("expected episode title preserved, got %q", req.EpisodeTitle)
	}
}

func TestReleaseNameRequestFromMetaOmitsSeriesTitleEpisodeTitle(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 2},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{TVDBID: 2, NameEnglish: "Re: ZERO, Starting Life in Another World"},
		},
		Type:         "ENCODE",
		SeasonStr:    "S04",
		EpisodeStr:   "E11",
		EpisodeTitle: "Re:ZERO -Starting Life in Another World-",
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.EpisodeTitle != "" {
		t.Fatalf("expected duplicate series episode title omitted, got %q", req.EpisodeTitle)
	}

	result := BuildReleaseName(req, api.NopLogger{})
	if strings.Contains(result.NameNoTag, "Re:ZERO -Starting Life in Another World-") {
		t.Fatalf("expected release name to omit duplicate episode title, got %q", result.NameNoTag)
	}
}

func TestReleaseNameRequestFromMetaTVPackOmitsSeasonTitle(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TMDBID: 1, TVDBID: 2},
		Release: api.ReleaseInfo{
			Title:      "Example Spy Show",
			Resolution: "2160p",
		},
		Type:         "WEBDL",
		Source:       "Web",
		Service:      "MGMP",
		Audio:        "DD+ 5.1",
		VideoEncode:  "H.265",
		SeasonStr:    "S01",
		TVPack:       true,
		EpisodeTitle: "Season 1",
		Tag:          "-XEBEC",
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.EpisodeTitle != "" {
		t.Fatalf("expected tv pack season title omitted from naming request, got %q", req.EpisodeTitle)
	}

	result := BuildReleaseName(req, api.NopLogger{})
	if strings.Contains(result.NameNoTag, "Season 1") {
		t.Fatalf("expected tv pack name to omit season title, got %q", result.NameNoTag)
	}
	if strings.Contains(result.NameNoTag, "2022") {
		t.Fatalf("expected tv pack name to omit ordinary tvdb year, got %q", result.NameNoTag)
	}
	if !containsAll(result.NameNoTag, []string{"Example Spy Show", "S01", "MGMP", "WEB-DL"}) {
		t.Fatalf("expected tv pack name to keep season and service tokens, got %q", result.NameNoTag)
	}
}

func TestReleaseNameRequestFromMetaFallsBackMovieCategory(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Movies\2026 - Example Movie [DVD9.PAL]`,
		DiscType:   "DVD",
		Type:       "DISC",
		Source:     "DVD",
		Release: api.ReleaseInfo{
			Title: "Example Movie",
			Year:  1982,
			Size:  "DVD9",
		},
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.Category != "MOVIE" {
		t.Fatalf("expected MOVIE category fallback, got %q", req.Category)
	}

	result := BuildReleaseName(req, api.NopLogger{})
	if result.NameNoTag == "" {
		t.Fatalf("expected release name to be built")
	}
}

func TestReleaseNameRequestFromMetaIgnoresUnsupportedReleaseCategory(t *testing.T) {
	testCases := []struct {
		category string
	}{
		{"MUSIC"},
		{"AUDIO"},
		{"EBOOK"},
	}

	for _, tc := range testCases {
		t.Run(tc.category, func(t *testing.T) {
			meta := api.PreparedMetadata{
				SourcePath: `D:\Movies\2026 - Example Movie [DVD9.PAL]`,
				DiscType:   "DVD",
				Type:       "DISC",
				Source:     "DVD",
				Release: api.ReleaseInfo{
					Category: tc.category,
					Title:    "Example Movie",
					Year:     1982,
					Size:     "DVD9",
				},
			}

			req := releaseNameRequestFromMeta(meta, api.NopLogger{})
			if req.Category != "MOVIE" {
				t.Fatalf("expected unsupported release category to fall back to MOVIE, got %q", req.Category)
			}

			result := BuildReleaseName(req, api.NopLogger{})
			if result.NameNoTag == "" {
				t.Fatalf("expected release name to be built after category fallback")
			}
		})
	}
}

func TestReleaseNameRequestFromMetaInfersTVFromPath(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath: `D:\Shows\Example.Show.S01E01.1080p.WEB-DL`,
		Type:       "ENCODE",
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.Category != "TV" {
		t.Fatalf("expected TV category fallback, got %q", req.Category)
	}
}

func TestBuildReleaseNameTVEpisodeAliasUsesSourceType(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "EPISODE",
		Title:       "Australian Survivor",
		Season:      "S14",
		Episode:     "E01",
		Resolution:  "1080p",
		Source:      "WEB-DL",
		Audio:       "AAC 2.0",
		VideoEncode: "H.264",
	}, api.NopLogger{})

	if result.NameNoTag == "" {
		t.Fatalf("expected release name for TV EPISODE alias")
	}
	if result.Name == "" {
		t.Fatalf("expected release name with tag handling")
	}
	if got := result.NameNoTag; !containsAll(got, []string{"Australian Survivor", "S14E01", "WEB-DL"}) {
		t.Fatalf("expected TV episode-style name, got %q", got)
	}
}

func TestBuildReleaseNameTVSeriesAliasFallsBackEncode(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "SERIES",
		Title:       "Example Show",
		Season:      "S02",
		Resolution:  "1080p",
		Source:      "Unknown",
		Audio:       "AAC",
		VideoEncode: "x264",
	}, api.NopLogger{})

	if result.NameNoTag == "" {
		t.Fatalf("expected fallback TV name for SERIES alias")
	}
	if got := result.NameNoTag; !containsAll(got, []string{"Example Show", "S02", "1080p", "x264"}) {
		t.Fatalf("expected fallback encode-style TV name, got %q", got)
	}
}

func TestResolveReleaseNameTitleTVDBEnglishWinsForTV(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TMDBID: 1, TVDBID: 2},
		Release:     api.ReleaseInfo{Title: "Release Name", Year: 2001},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "TMDB Name", OriginalTitle: "TMDB Original", Year: 2010},
			TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "TVDB Native", NameEnglish: "TVDB English", Year: 2012, YearFromAlias: true},
		},
	}

	title, altTitle, year := resolveReleaseNameTitle("TV", meta)
	if title != "TVDB English" {
		t.Fatalf("expected english tvdb title, got %q", title)
	}
	if year != 2012 {
		t.Fatalf("expected tvdb year, got %d", year)
	}
	if altTitle != "AKA TVDB Native" {
		t.Fatalf("expected native TVDB alternate title, got %q", altTitle)
	}
}

func TestResolveReleaseNameTitleTVDBFallsBackToOriginalWhenEnglishMissing(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TMDBID: 1, TVDBID: 2},
		Release:     api.ReleaseInfo{Title: "Release Name", Year: 2001},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "TMDB Name", OriginalTitle: "TMDB Original", Year: 2010},
			TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "TVDB Native", Year: 2012},
		},
	}

	title, altTitle, year := resolveReleaseNameTitle("TV", meta)
	if title != "TVDB Native" {
		t.Fatalf("expected original tvdb title fallback, got %q", title)
	}
	if year != 0 {
		t.Fatalf("expected tv year omitted when not alias-derived, got %d", year)
	}
	if altTitle != "" {
		t.Fatalf("expected no tmdb aka when tvdb precedence is active, got %q", altTitle)
	}
}

func TestReleaseNameRequestFromMetaMovieUsesIMDbWithoutTMDB(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Movies\Parsed.Title.2026.1080p.BluRay.x264-GRP.mkv`,
		ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 1234567},
		Type:        "ENCODE",
		Source:      "BluRay",
		Audio:       "AAC 2.0",
		VideoEncode: "x264",
		Tag:         "-GRP",
		Release:     api.ReleaseInfo{Title: "Parsed Title", Resolution: "1080p"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "IMDb Title", AKA: "Original Title", Year: 2026},
		},
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.Title != "IMDb Title" || req.AltTitle != "AKA Original Title" || req.Year != 2026 {
		t.Fatalf("unexpected IMDb naming fields: title=%q alt=%q year=%d", req.Title, req.AltTitle, req.Year)
	}
	result := BuildReleaseName(req, api.NopLogger{})
	if !containsAll(result.NameNoTag, []string{"IMDb Title", "AKA Original Title", "2026"}) {
		t.Fatalf("expected rebuilt IMDb-only name, got %q", result.NameNoTag)
	}
}

func TestResolveReleaseNameTitleProviderAlternateRules(t *testing.T) {
	tests := []struct {
		name       string
		releaseAlt string
		imdbAKA    string
		wantAlt    string
	}{
		{name: "same as title", imdbAKA: "IMDb Title"},
		{name: "normalizes prefix", imdbAKA: "AKA Original Title", wantAlt: "AKA Original Title"},
		{name: "preserves parsed alternate", releaseAlt: "AKA Parsed Alternate", imdbAKA: "Original Title", wantAlt: "AKA Parsed Alternate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "MOVIE", IMDBID: 1234567},
				Release:     api.ReleaseInfo{Title: "Parsed Title", Alt: tc.releaseAlt},
				ExternalMetadata: api.ExternalMetadata{
					IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "IMDb Title", AKA: tc.imdbAKA},
				},
			}
			_, got, _ := resolveReleaseNameTitle("MOVIE", meta)
			if got != tc.wantAlt {
				t.Fatalf("alternate=%q, want %q", got, tc.wantAlt)
			}
		})
	}
}

func TestResolveReleaseNameTitlePrefersTMDBForMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE", TMDBID: 1, IMDBID: 1234567},
		Release:     api.ReleaseInfo{Title: "Parsed Title"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "TMDB Title", OriginalTitle: "TMDB Original", Year: 2025},
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "IMDb Title", AKA: "IMDb Original", Year: 2026},
		},
	}
	title, alt, year := resolveReleaseNameTitle("MOVIE", meta)
	if title != "TMDB Title" || alt != "AKA TMDB Original" || year != 2025 {
		t.Fatalf("unexpected TMDB-preferred fields: title=%q alt=%q year=%d", title, alt, year)
	}
}

func TestResolveReleaseNameTitleTVFallsBackFromUnusableTVDBToIMDb(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", IMDBID: 1234567, TVDBID: 2},
		Release:     api.ReleaseInfo{Title: "Parsed Series"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "IMDb Series", AKA: "Original Series", Year: 2026},
			TVDB: &api.TVDBMetadata{TVDBID: 3, NameEnglish: "Wrong Series"},
		},
	}
	title, alt, year := resolveReleaseNameTitle("TV", meta)
	if title != "IMDb Series" || alt != "AKA Original Series" || year != 2026 {
		t.Fatalf("unexpected IMDb fallback fields: title=%q alt=%q year=%d", title, alt, year)
	}
}

func TestReleaseNameRequestFromMetaTVUsesIMDbWithoutTVDBOrTMDB(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Shows\Parsed.Series.S01E01.1080p.WEB-DL.x264-GRP.mkv`,
		ExternalIDs: api.ExternalIDs{Category: "TV", IMDBID: 1234567},
		Type:        "WEBDL",
		Source:      "Web",
		Audio:       "AAC 2.0",
		VideoEncode: "x264",
		SeasonStr:   "S01",
		EpisodeStr:  "E01",
		Tag:         "-GRP",
		Release:     api.ReleaseInfo{Title: "Parsed Series", Resolution: "1080p"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "IMDb Series", AKA: "Original Series", Year: 2026},
		},
	}
	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.SearchYear != "2026" {
		t.Fatalf("expected IMDb TV search year, got %q", req.SearchYear)
	}
	result := BuildReleaseName(req, api.NopLogger{})
	if !containsAll(result.NameNoTag, []string{"IMDb Series", "AKA Original Series", "2026", "S01E01"}) {
		t.Fatalf("expected rebuilt IMDb-only TV name, got %q", result.NameNoTag)
	}
}

func TestResolveReleaseNameTitleIgnoresTVmazeOnlyMetadata(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVmazeID: 3},
		Release:     api.ReleaseInfo{Title: "Parsed Series", Year: 2025},
		ExternalMetadata: api.ExternalMetadata{
			TVmaze: &api.TVmazeMetadata{TVmazeID: 3, Name: "TVmaze Series", Premiered: "2026-01-01"},
		},
	}
	title, alt, year := resolveReleaseNameTitle("TV", meta)
	if title != "Parsed Series" || alt != "" || year != 2025 {
		t.Fatalf("TVmaze changed shared naming: title=%q alt=%q year=%d", title, alt, year)
	}
}

func TestResolveReleaseNameTitleIgnoresStaleProviderMetadata(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  "current-source",
		ExternalIDs: api.ExternalIDs{SourcePath: "current-source", Category: "MOVIE", IMDBID: 1234567},
		Release:     api.ReleaseInfo{Title: "Parsed Title", Year: 2025},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "stale-source",
			IMDB:       &api.IMDBMetadata{IMDBID: 1234567, Title: "Stale IMDb Title", AKA: "Stale Original", Year: 2026},
		},
	}
	title, alt, year := resolveReleaseNameTitle("MOVIE", meta)
	if title != "Parsed Title" || alt != "" || year != 2025 {
		t.Fatalf("stale metadata changed naming: title=%q alt=%q year=%d", title, alt, year)
	}
}

func TestReleaseNameRequestFromMetaTVSearchYearComesFromTVDB(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Shows\Example.Show.S01E01.1080p.BluRay.x264`,
		ExternalIDs: api.ExternalIDs{Category: "TV", TMDBID: 1, TVDBID: 2},
		Type:        "ENCODE",
		Source:      "BluRay",
		Audio:       "AAC 2.0",
		VideoEncode: "x264",
		Release:     api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "TMDB Name", Year: 2010},
			TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "TVDB Name", Year: 2024, YearFromAlias: true},
		},
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.SearchYear != "2024" {
		t.Fatalf("expected tv search year from tvdb, got %q", req.SearchYear)
	}
	result := BuildReleaseName(req, api.NopLogger{})
	if !strings.Contains(result.NameNoTag, "2024") {
		t.Fatalf("expected tv release name to include tvdb year, got %q", result.NameNoTag)
	}
}

func TestReleaseNameRequestFromMetaTVStripsBracketedTVDBYear(t *testing.T) {
	sourceName := "Example.Show.2024.S01.720p.NF.WEB-DL.DDP5.1.x264-GRP"
	meta := api.PreparedMetadata{
		SourcePath:  sourceName,
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 2},
		Type:        "WEBDL",
		Source:      "Web",
		Audio:       "DD+ 5.1 Atmos",
		VideoEncode: "H.264",
		Service:     "NF",
		SeasonStr:   "S01",
		TVPack:      true,
		Release: api.ReleaseInfo{
			Title:      "Example Show",
			Year:       2024,
			Resolution: "720p",
		},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{TVDBID: 2, NameEnglish: "Example Show (2024)", Year: 2024, YearFromAlias: true},
		},
		Tag: "-GRP",
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.Title != "Example Show" {
		t.Fatalf("expected bracketed year stripped from tvdb title, got %q", req.Title)
	}
	if req.SearchYear != "2024" {
		t.Fatalf("expected tv search year, got %q", req.SearchYear)
	}
	result := BuildReleaseName(req, api.NopLogger{})
	expected := "Example Show 2024 S01 720p NF WEB-DL DD+ 5.1 Atmos H.264-GRP"
	if result.Name != expected {
		t.Fatalf("expected release name %q, got %q", expected, result.Name)
	}
}

func TestReleaseNameRequestFromMetaTVOmitsSearchYearWhenTVDBYearNotAliasDerived(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Shows\Example.Show.S01E01.1080p.BluRay.x264`,
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 2},
		Type:        "ENCODE",
		Source:      "BluRay",
		Audio:       "AAC 2.0",
		VideoEncode: "x264",
		Release:     api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "TVDB Name", Year: 2024},
		},
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.SearchYear != "" {
		t.Fatalf("expected empty tv search year when tvdb year is not alias-derived, got %q", req.SearchYear)
	}
}

func TestReleaseNameRequestFromMetaLogsTVDBYearSource(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Shows\Example.Show.S01E01.1080p.BluRay.x264`,
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 2},
		Type:        "ENCODE",
		Source:      "BluRay",
		Release:     api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{TVDBID: 2, Name: "TVDB Name", Year: 2024, YearSource: "first_aired"},
		},
	}
	logger := &captureLogger{}

	req := releaseNameRequestFromMeta(meta, logger)
	if req.SearchYear != "" {
		t.Fatalf("expected empty tv search year, got %q", req.SearchYear)
	}
	if !logger.contains(`year_source="first_aired"`) {
		t.Fatalf("expected year source in trace logs, got %#v", logger.lines)
	}
	if !logger.contains(`tvdb_year_from_alias=false`) {
		t.Fatalf("expected tvdb_year_from_alias=false in trace logs, got %#v", logger.lines)
	}
}

func TestBuildReleaseNameTVUsesSearchYearOverRequestYear(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "ENCODE",
		Title:       "Example Show",
		Year:        1999,
		SearchYear:  "2024",
		Season:      "S01",
		Episode:     "E01",
		Resolution:  "1080p",
		Source:      "BluRay",
		Audio:       "AAC",
		VideoEncode: "x264",
	}, api.NopLogger{})

	if !strings.Contains(result.NameNoTag, "2024") {
		t.Fatalf("expected tv release name to include search year, got %q", result.NameNoTag)
	}
	if strings.Contains(result.NameNoTag, "1999") {
		t.Fatalf("expected tv release name to ignore request year when search year is set, got %q", result.NameNoTag)
	}
}

func TestBuildReleaseNameTVStripsDuplicateParentheticalSearchYear(t *testing.T) {
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Example Show (2024)",
		SearchYear:  "2024",
		Season:      "S01",
		Resolution:  "1080p",
		Service:     "NF",
		Audio:       "AAC 2.0",
		VideoEncode: "H.264",
		Tag:         "-GRP",
	}, api.NopLogger{})

	expected := "Example Show 2024 S01 1080p NF WEB-DL AAC 2.0 H.264-GRP"
	if result.Name != expected {
		t.Fatalf("expected release name %q, got %q", expected, result.Name)
	}
}

func TestReleaseNameRequestFromMetaMovieKeepsParsedYearWhenTVDBMetadataPresent(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Movies\Example.Movie.2026.720p.BluRay.x264-GRP.mkv`,
		ExternalIDs: api.ExternalIDs{Category: "MOVIE", TMDBID: 1},
		Type:        "ENCODE",
		Source:      "BluRay",
		Audio:       "DD 5.1",
		VideoCodec:  "AVC",
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2026,
			Resolution: "720p",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Movie", Year: 2026},
			TVDB: &api.TVDBMetadata{},
		},
	}

	req := releaseNameRequestFromMeta(meta, api.NopLogger{})
	if req.Year != 2026 {
		t.Fatalf("expected movie request year to remain parsed year, got %d", req.Year)
	}

	result := BuildReleaseName(req, api.NopLogger{})
	if !strings.Contains(result.NameNoTag, "2026") {
		t.Fatalf("expected movie release name to include parsed year, got %q", result.NameNoTag)
	}
}

func TestApplyReleaseNameOverridesUseSeasonEpisodeFallsBackToDailyWhenTMDBMissing(t *testing.T) {
	base := api.ReleaseNameRequest{
		Category:      "TV",
		DailyDate:     "2025-11-10",
		ManualDate:    true,
		TMDBDateMatch: false,
	}
	updated := applyReleaseNameOverrides(base, api.ReleaseNameOverrides{UseSeasonEpisode: new(true)}, api.NopLogger{})
	if !updated.ManualDate {
		t.Fatalf("expected daily-date mode to remain enabled when tmdb mapping is unavailable")
	}
}

func TestApplyReleaseNameOverridesUseSeasonEpisodeUsesTMDBMatch(t *testing.T) {
	base := api.ReleaseNameRequest{
		Category:      "TV",
		DailyDate:     "2025-11-10",
		ManualDate:    true,
		TMDBDateMatch: true,
	}
	updated := applyReleaseNameOverrides(base, api.ReleaseNameOverrides{UseSeasonEpisode: new(true)}, api.NopLogger{})
	if updated.ManualDate {
		t.Fatalf("expected season/episode mode when tmdb mapping is available")
	}
}

func containsAll(value string, parts []string) bool {
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

type captureLogger struct {
	lines []string
}

func (l *captureLogger) Tracef(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Debugf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Infof(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Warnf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) Errorf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *captureLogger) contains(value string) bool {
	for _, line := range l.lines {
		if strings.Contains(line, value) {
			return true
		}
	}
	return false
}

func TestRebuildReleaseNameFoldsTagOverrideIntoMeta(t *testing.T) {
	tag := "GRP"
	noTag := true

	meta := api.PreparedMetadata{
		Type:        "REMUX",
		Source:      "BluRay",
		VideoCodec:  "AVC",
		SeasonStr:   "S01",
		Tag:         "",
		Release:     api.ReleaseInfo{Title: "The Show", Year: 2021, Resolution: "1080p"},
		ExternalIDs: api.ExternalIDs{Category: "TV"},
	}
	meta.ReleaseNameOverrides = api.ReleaseNameOverrides{Tag: &tag}

	RebuildReleaseName(&meta, nil)
	if meta.Tag != "-GRP" {
		t.Fatalf("expected tag override folded into meta.Tag, got %q", meta.Tag)
	}
	if meta.Release.Group != "GRP" {
		t.Fatalf("expected tag override folded into meta.Release.Group (resolveGroup prefers it), got %q", meta.Release.Group)
	}
	if !strings.HasSuffix(meta.ReleaseName, "-GRP") {
		t.Fatalf("expected release name to carry the tag, got %q", meta.ReleaseName)
	}

	meta.ReleaseNameOverrides.NoTag = &noTag
	RebuildReleaseName(&meta, nil)
	if meta.Tag != "" {
		t.Fatalf("expected NoTag to clear meta.Tag, got %q", meta.Tag)
	}
}

func TestRebuildReleaseNameFoldsValueOverridesIntoMeta(t *testing.T) {
	category := "TV"
	releaseType := "REMUX"
	source := "BluRay"
	resolution := "1080p"
	service := "NF"
	edition := "Extended"
	region := "EUR"
	season := "S02"
	episode := "E07"
	episodeTitle := "The Finale"
	year := 2019
	dualAudio := true

	meta := api.PreparedMetadata{
		Type:        "ENCODE",
		Source:      "WEB",
		Service:     "AMZN",
		Edition:     "Theatrical",
		Region:      "USA",
		SeasonStr:   "S01",
		SeasonInt:   1,
		EpisodeStr:  "E01",
		EpisodeInt:  1,
		Audio:       "FLAC 2.0",
		VideoCodec:  "AVC",
		Release:     api.ReleaseInfo{Title: "The Show", Year: 2021, Resolution: "2160p"},
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
	}
	meta.ReleaseNameOverrides = api.ReleaseNameOverrides{
		Category:     &category,
		Type:         &releaseType,
		Source:       &source,
		Resolution:   &resolution,
		Service:      &service,
		Edition:      &edition,
		Region:       &region,
		Season:       &season,
		Episode:      &episode,
		EpisodeTitle: &episodeTitle,
		ManualYear:   &year,
		DualAudio:    &dualAudio,
	}

	RebuildReleaseName(&meta, nil)

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"ExternalIDs.Category", meta.ExternalIDs.Category, "TV"},
		{"Type", meta.Type, "REMUX"},
		{"Source", meta.Source, "BluRay"},
		{"Release.Resolution", meta.Release.Resolution, "1080p"},
		{"Service", meta.Service, "NF"},
		{"Edition", meta.Edition, "Extended"},
		{"Region", meta.Region, "EUR"},
		{"SeasonStr", meta.SeasonStr, "S02"},
		{"EpisodeStr", meta.EpisodeStr, "E07"},
		{"EpisodeTitle", meta.EpisodeTitle, "The Finale"},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s: got %q, want %q", check.field, check.got, check.want)
		}
	}
	if meta.SeasonInt != 2 || meta.EpisodeInt != 7 {
		t.Errorf("season/episode numbers: got S%d E%d, want S2 E7", meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.Release.Year != 2019 {
		t.Errorf("Release.Year: got %d, want 2019", meta.Release.Year)
	}
	if !strings.Contains(meta.Audio, "Dual-Audio") {
		t.Errorf("Audio: expected Dual-Audio marker, got %q", meta.Audio)
	}
}
