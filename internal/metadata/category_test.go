// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"reflect"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceCategoryDetection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"show folder", filepath.Join("media", "shows", "Example.Movie.1080p.mkv"), "TV"},
		{"stored Windows path", `C:\Media\TV Shows\Example.Movie.1080p.mkv`, "TV"},
		{"tv shows folder", filepath.Join("media", "TV Shows", "Example.Movie.1080p.mkv"), "TV"},
		{"season parent", filepath.Join("media", "Example Series (2005)", "Season 01 [DVD]", "Example.Series.DVD.x264.mkv"), "TV"},
		{"four-digit nonyear season", "Example.Show.S0100E03.1080p.mkv", "TV"},
		{"four-digit maximum season", "Example.Show.S9999E03.1080p.mkv", "TV"},
		{"year season parent", filepath.Join("media", "Example Series", "S2026", "Example.Series.mkv"), "TV"},
		{"tv pack parent", filepath.Join("media", "Example Series TV Pack", "Example.Series.mkv"), "TV"},
		{"episode prefix", "Example.Series.E03 - Pilot.mkv", "TV"},
		{"daily filename", "Example.Show.2026.3.4.mkv", "TV"},
		{"anime 720p", "[SubsPlease] Example Series - 03 (720p).mkv", "TV"},
		{"anime interlaced", "[SubsPlease] Example Series - 03 (480i).mkv", "TV"},
		{"rls fallback", "Example.Series.E03.1080p.mkv", "TV"},
		{"unknown stays weak evidence", "Example Release", ""},
		{"movie", filepath.Join("media", "movies", "Example.Movie.2005.1080p.mkv"), "MOVIE"},
		{"wrong season width", filepath.Join("media", "S123", "Example.Movie.1080p.mkv"), "MOVIE"},
		{"dimensions", "Example.Movie.1920x800.1080p.mkv", "MOVIE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseReleaseInfo(tc.path).Category; got != tc.want {
				t.Fatalf("category = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourceCategoryLeavesBasenameFactsUnchanged(t *testing.T) {
	t.Parallel()
	base := "Example.Series.DVD.x264.AC3-GRP.mkv"
	input := filepath.Join("media", "Example Series (2005)", "Season 01 [DVD]", base)
	got, want := ParseReleaseInfo(input), ParseReleaseInfo(base)
	if got.Category != "TV" {
		t.Fatalf("category = %q, want TV", got.Category)
	}
	got.Category = want.Category
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parent context changed basename release facts: got=%+v want=%+v", got, want)
	}
}

func TestCategoryPreferencePreservesExplicitAndTrackerAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		manual    *string
		tracker   api.Category
		parsed    string
		mediaInfo string
		want      string
	}{
		{"explicit movie", new("MOVIE"), "TV", "TV", "TV", "MOVIE"},
		{"explicit tv", new("TV"), "MOVIE", "MOVIE", "MOVIE", "TV"},
		{"tracker tv before parsed movie", nil, "TV", "MOVIE", "", "TV"},
		{"tracker tv before media movie", nil, "TV", "", "MOVIE", "TV"},
		{"tracker movie before source tv", nil, "MOVIE", "TV", "TV", "MOVIE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := preparationstate.State{
				SourcePath:           filepath.Join("media", "tv", "Example.Release.mkv"),
				Release:              api.ReleaseInfo{Category: tc.parsed},
				MediaInfoCategory:    tc.mediaInfo,
				ReleaseNameOverrides: api.ReleaseNameOverrides{Category: tc.manual},
				TrackerData:          []api.TrackerMetadata{{Category: tc.tracker}},
			}
			if got := resolveCategoryPreference(meta); got != tc.want {
				t.Fatalf("category = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnknownSourceCategoryFallsBackOnlyForNaming(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{SourcePath: "Example Release", Release: ParseReleaseInfo("Example Release")}
	if meta.Release.Category != "" {
		t.Fatalf("unknown parser category became strong evidence: %q", meta.Release.Category)
	}
	if got := releaseNameRequestFromMeta(meta, api.NopLogger{}).Category; got != "MOVIE" {
		t.Fatalf("final category = %q, want MOVIE", got)
	}
}
