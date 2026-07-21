// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBestSceneCandidate(t *testing.T) {
	t.Parallel()

	manualTag := "-GRP"
	cases := []struct {
		name      string
		meta      preparationstate.State
		release   api.ReleaseInfo
		tag       string
		overrides api.ReleaseNameOverrides
		localBase string
		cands     []srrdbSearchResult
		wantPick  string // "" means expect no confident match
	}{
		{
			name: "exact tokens (renamed dots to spaces) match",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "GRP",
				Source:     "BluRay",
				Codec:      []string{"x264"},
			},
			localBase: "Example Movie 2026 1080p BluRay x264 GRP",
			cands: []srrdbSearchResult{
				// Same title at a different resolution must not be matched.
				{Release: "Example.Movie.2026.720p.BluRay.x264-GRP"},
				{Release: "Example.Movie.2026.1080p.BluRay.x264-GRP"},
			},
			wantPick: "Example.Movie.2026.1080p.BluRay.x264-GRP",
		},
		{
			name: "foreign dub is not chosen for an english release",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "GRP",
				Language:   []string{"English"},
			},
			localBase: "Example Drama 2026 1080p BluRay x264 GRP",
			cands: []srrdbSearchResult{
				{Release: "Example.Drama.2026.German.DL.1080p.BluRay.x264-GRP", IsForeign: "yes"},
				{Release: "Example.Drama.2026.1080p.BluRay.x264-GRP", IsForeign: "no"},
			},
			wantPick: "Example.Drama.2026.1080p.BluRay.x264-GRP",
		},
		{
			name: "multi-edition prefers the matching theatrical cut",
			release: api.ReleaseInfo{
				Resolution: "2160p",
				Year:       2014,
				Group:      "GRP",
			},
			localBase: "Movie 2014 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.Extended.2160p.BluRay.x265-GRP"},
				{Release: "Movie.2014.2160p.BluRay.x265-GRP"},
			},
			wantPick: "Movie.2014.2160p.BluRay.x265-GRP",
		},
		{
			name: "multi-edition prefers the matching extended cut",
			release: api.ReleaseInfo{
				Resolution: "2160p",
				Year:       2014,
				Group:      "GRP",
				Edition:    []string{"Extended"},
			},
			localBase: "Movie 2014 Extended 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.2160p.BluRay.x265-GRP"},
				{Release: "Movie.2014.Extended.2160p.BluRay.x265-GRP"},
			},
			wantPick: "Movie.2014.Extended.2160p.BluRay.x265-GRP",
		},
		{
			name: "season pack matches on resolution and group without a year",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Group:      "GRP",
				Source:     "WEB-DL",
			},
			localBase: "Show S01 1080p WEB-DL GRP",
			cands: []srrdbSearchResult{
				{Release: "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP"},
				{Release: "Other.Show.S01.720p.WEB-DL-GRP"},
			},
			wantPick: "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP",
		},
		{
			name: "english web-dl is not misclassified as a foreign dub",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "GRP",
				Source:     "WEB-DL",
				Language:   []string{"English"},
			},
			localBase: "Movie 2026 1080p WEB-DL DDP5 1 H 264 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2026.German.DL.1080p.BluRay.x264-GRP", IsForeign: "yes"},
				{Release: "Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", IsForeign: "no"},
			},
			wantPick: "Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
		},
		{
			name:      "year-only agreement with unknown resolution is not confident",
			release:   api.ReleaseInfo{Year: 2026, Group: "P2PGRP"},
			localBase: "Movie 2026 DVDRip x264 P2PGRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2026.R5.XviD-OTHER"},
			},
			wantPick: "",
		},
		{
			name: "known local group must match candidate group",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "EXAMPLE",
				Source:     "WEB-DL",
				Codec:      []string{"H.264"},
			},
			localBase: "Example Sports Movie 2026 1080p AMZN WEB-DL DD+ 5.1 H.264-EXAMPLE",
			cands: []srrdbSearchResult{
				{Release: "Example.Sports.Movie.2026.1080p.WEB.H264-OTHER", IsForeign: "no"},
			},
			wantPick: "",
		},
		{
			name: "known local group match is case-insensitive",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "example",
				Source:     "WEB-DL",
				Codec:      []string{"H.264"},
			},
			localBase: "Movie 2026 1080p WEB-DL H.264-example",
			cands: []srrdbSearchResult{
				{Release: "Movie.2026.1080p.WEB-DL.H.264-EXAMPLE", IsForeign: "no"},
			},
			wantPick: "Movie.2026.1080p.WEB-DL.H.264-EXAMPLE",
		},
		{
			name: "manual tag override wins over parsed filename group",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2026,
				Group:      "example",
				Source:     "BluRay",
				Codec:      []string{"x264"},
			},
			tag:       "-example",
			overrides: api.ReleaseNameOverrides{Tag: &manualTag},
			localBase: "renamed-example",
			cands: []srrdbSearchResult{
				{Release: "Example.Driver.2026.1080p.BluRay.x264-GRP", IsForeign: "no"},
			},
			wantPick: "Example.Driver.2026.1080p.BluRay.x264-GRP",
		},
		{
			name: "external metadata year participates when parser year is missing",
			meta: preparationstate.State{
				Release:          api.ReleaseInfo{Resolution: "1080p"},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
			},
			localBase: "renamed-example",
			cands: []srrdbSearchResult{
				{Release: "Example.Driver.2026.1080p.BluRay.x264-GRP", IsForeign: "no"},
			},
			wantPick: "Example.Driver.2026.1080p.BluRay.x264-GRP",
		},
		{
			name: "media-derived codec participates when parser codec is missing",
			meta: preparationstate.State{
				Release:          api.ReleaseInfo{Resolution: "1080p"},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
				VideoEncode:      "x264",
			},
			localBase: "Example Driver 2026 1080p",
			cands: []srrdbSearchResult{
				{Release: "Example.Driver.2026.1080p.BluRay.x265-OTHER", IsForeign: "no"},
				{Release: "Example.Driver.2026.1080p.BluRay.x264-GRP", IsForeign: "no"},
			},
			wantPick: "Example.Driver.2026.1080p.BluRay.x264-GRP",
		},
		{
			name: "no candidate at the right resolution is not matched",
			release: api.ReleaseInfo{
				Resolution: "2160p",
				Year:       2014,
				Group:      "GRP",
			},
			localBase: "Movie 2014 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.1080p.BluRay.x264-GRP"},
				{Release: "Movie.2014.720p.BluRay.x264-GRP"},
			},
			wantPick: "",
		},
		{
			name: "wrong year and group is not matched",
			release: api.ReleaseInfo{
				Resolution: "1080p",
				Year:       2014,
				Group:      "GRP",
			},
			localBase: "Movie 2014 1080p BluRay x264 GRP",
			cands: []srrdbSearchResult{
				{Release: "Different.Movie.1999.1080p.BluRay.x264-OTHER"},
			},
			wantPick: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := preparationstate.State{
				Release:              tc.release,
				Tag:                  tc.tag,
				ReleaseNameOverrides: tc.overrides,
			}
			if tc.meta.SourcePath != "" ||
				tc.meta.VideoPath != "" ||
				tc.meta.Release.Resolution != "" ||
				tc.meta.ProviderMetadata.TMDB != nil ||
				tc.meta.ProviderMetadata.IMDB != nil ||
				tc.meta.ProviderMetadata.TVDB != nil ||
				tc.meta.ProviderMetadata.TVmaze != nil ||
				tc.meta.VideoEncode != "" ||
				tc.meta.VideoCodec != "" {
				meta = tc.meta
			}
			best, score := bestSceneCandidate(meta, tc.localBase, tc.cands)
			if tc.wantPick == "" {
				if best != nil {
					t.Fatalf("expected no confident match, got %q (score %d)", best.Release, score)
				}
				return
			}
			if best == nil {
				t.Fatalf("expected match %q, got none", tc.wantPick)
			}
			if best.Release != tc.wantPick {
				t.Fatalf("picked %q (score %d), want %q", best.Release, score, tc.wantPick)
			}
		})
	}
}

func TestArchivedMediaRenamed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		archived    []srrdbArchivedFile
		localMedia  string
		wantRenamed bool
		wantMatched bool
	}{
		{
			name: "renamed local filename is flagged",
			archived: []srrdbArchivedFile{
				{Name: "example.driver.2026.1080p.bluray.x264-grp.nfo", Size: 100},
				{Name: "example.driver.2026.1080p.bluray.x264-grp.mkv", Size: 8000000000},
			},
			localMedia:  "renamed-example.mkv",
			wantRenamed: true,
			wantMatched: true,
		},
		{
			name: "exact case-sensitive filename match is not renamed",
			archived: []srrdbArchivedFile{
				{Name: "example.driver.2026.1080p.bluray.x264-grp.mkv", Size: 8000000000},
			},
			localMedia:  "example.driver.2026.1080p.bluray.x264-grp.mkv",
			wantRenamed: false,
			wantMatched: true,
		},
		{
			name: "casing-only difference is treated as renamed (srrdb authoritative)",
			archived: []srrdbArchivedFile{
				{Name: "example.driver.2026.1080p.bluray.x264-grp.mkv", Size: 8000000000},
			},
			localMedia:  "Example.Driver.2026.1080p.BluRay.x264-GRP.mkv",
			wantRenamed: true,
			wantMatched: true,
		},
		{
			name: "season pack: local episode matching a canonical file is not renamed",
			archived: []srrdbArchivedFile{
				{Name: "show.s01e01.1080p.web-dl-grp.mkv", Size: 2000000000},
				{Name: "show.s01e02.1080p.web-dl-grp.mkv", Size: 2100000000},
			},
			localMedia:  "show.s01e02.1080p.web-dl-grp.mkv",
			wantRenamed: false,
			wantMatched: true,
		},
		{
			name: "no archived media member yields no verdict",
			archived: []srrdbArchivedFile{
				{Name: "release.nfo", Size: 100},
				{Name: "sample/something.txt", Size: 10},
			},
			localMedia:  "whatever.mkv",
			wantRenamed: false,
			wantMatched: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			renamed, matched := archivedMediaRenamed(tc.archived, tc.localMedia)
			if renamed != tc.wantRenamed || matched != tc.wantMatched {
				t.Fatalf("archivedMediaRenamed = (renamed=%t matched=%t), want (renamed=%t matched=%t)", renamed, matched, tc.wantRenamed, tc.wantMatched)
			}
		})
	}
}

func TestFormatSRRDBIMDbID(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		12345:   "tt0012345",
		1234567: "tt1234567",
		7654321: "tt7654321",
		0:       "",
		-5:      "",
	}
	for id, want := range cases {
		if got := formatSRRDBIMDbID(id); got != want {
			t.Fatalf("formatSRRDBIMDbID(%d) = %q, want %q", id, got, want)
		}
	}
}

func TestSceneLocalCandidates(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	// Folder release: SourcePath is the release folder, VideoPath the media file.
	const folder = "Example.Driver.2026.1080p.BluRay.x264-GRP"
	c := sceneLocalCandidates(preparationstate.State{
		SourcePath: filepath.Join(base, folder),
		VideoPath:  filepath.Join(base, folder, "renamed-example.mkv"),
	})
	if len(c.folders) != 1 || c.folders[0] != folder {
		t.Fatalf("folder candidates = %v, want [%s]", c.folders, folder)
	}
	if len(c.files) != 1 || c.files[0] != "renamed-example" {
		t.Fatalf("file candidates = %v, want [renamed-example]", c.files)
	}
	if c.mediaFilename != "renamed-example.mkv" {
		t.Fatalf("mediaFilename = %q, want renamed-example.mkv", c.mediaFilename)
	}

	// Single-file release: SourcePath == VideoPath, no folder candidate.
	singlePath := filepath.Join(base, "movie.2020.1080p.bluray.x264-grp.mkv")
	single := sceneLocalCandidates(preparationstate.State{SourcePath: singlePath, VideoPath: singlePath})
	if len(single.folders) != 0 {
		t.Fatalf("single-file folder candidates = %v, want none", single.folders)
	}
	if len(single.files) != 1 || single.files[0] != "movie.2020.1080p.bluray.x264-grp" {
		t.Fatalf("single-file file candidates = %v", single.files)
	}
}
