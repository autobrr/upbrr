// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBestSceneCandidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		release   api.ReleaseInfo
		tag       string
		localBase string
		cands     []srrdbSearchResult
		wantPick  string // "" means expect no confident match
	}{
		{
			name:      "exact tokens (renamed dots to spaces) match",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 2014, Group: "GRP", Source: "BluRay", Codec: []string{"x264"}},
			localBase: "Fury 2014 1080p BluRay x264 GRP",
			cands: []srrdbSearchResult{
				// Same title at a different resolution must not be matched.
				{Release: "Fury.2014.720p.BluRay.x264-GRP"},
				{Release: "Fury.2014.1080p.BluRay.x264-GRP"},
			},
			wantPick: "Fury.2014.1080p.BluRay.x264-GRP",
		},
		{
			name:      "foreign dub is not chosen for an english release",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 1994, Group: "GRP", Language: []string{"English"}},
			localBase: "The Shawshank Redemption 1994 1080p BluRay x264 GRP",
			cands: []srrdbSearchResult{
				{Release: "The.Shawshank.Redemption.1994.German.DL.1080p.BluRay.x264-GRP", IsForeign: "yes"},
				{Release: "The.Shawshank.Redemption.1994.1080p.BluRay.x264-GRP", IsForeign: "no"},
			},
			wantPick: "The.Shawshank.Redemption.1994.1080p.BluRay.x264-GRP",
		},
		{
			name:      "multi-edition prefers the matching theatrical cut",
			release:   api.ReleaseInfo{Resolution: "2160p", Year: 2014, Group: "GRP"},
			localBase: "Movie 2014 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.Extended.2160p.BluRay.x265-GRP"},
				{Release: "Movie.2014.2160p.BluRay.x265-GRP"},
			},
			wantPick: "Movie.2014.2160p.BluRay.x265-GRP",
		},
		{
			name:      "multi-edition prefers the matching extended cut",
			release:   api.ReleaseInfo{Resolution: "2160p", Year: 2014, Group: "GRP", Edition: []string{"Extended"}},
			localBase: "Movie 2014 Extended 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.2160p.BluRay.x265-GRP"},
				{Release: "Movie.2014.Extended.2160p.BluRay.x265-GRP"},
			},
			wantPick: "Movie.2014.Extended.2160p.BluRay.x265-GRP",
		},
		{
			name:      "season pack matches on resolution and group without a year",
			release:   api.ReleaseInfo{Resolution: "1080p", Group: "GRP", Source: "WEB-DL"},
			localBase: "Show S01 1080p WEB-DL GRP",
			cands: []srrdbSearchResult{
				{Release: "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP"},
				{Release: "Other.Show.S01.720p.WEB-DL-GRP"},
			},
			wantPick: "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP",
		},
		{
			name:      "english web-dl is not misclassified as a foreign dub",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 2019, Group: "YIFY", Source: "WEB-DL", Language: []string{"English"}},
			localBase: "Movie 2019 1080p WEB-DL DDP5 1 H 264 YIFY",
			cands: []srrdbSearchResult{
				{Release: "Movie.2019.German.DL.1080p.BluRay.x264-YIFY", IsForeign: "yes"},
				{Release: "Movie.2019.1080p.WEB-DL.DDP5.1.H.264-YIFY", IsForeign: "no"},
			},
			wantPick: "Movie.2019.1080p.WEB-DL.DDP5.1.H.264-YIFY",
		},
		{
			name:      "year-only agreement with unknown resolution is not confident",
			release:   api.ReleaseInfo{Year: 2008, Group: "P2PGROUP"},
			localBase: "Movie 2008 DVDRip x264 P2PGROUP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2008.R5.XviD-SCENEGROUP"},
			},
			wantPick: "",
		},
		{
			name:      "known local group must match candidate group",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 2015, Group: "monkee", Source: "WEB-DL", Codec: []string{"H.264"}},
			localBase: "7 Days in Hell 2015 1080p AMZN WEB-DL DD+ 5.1 H.264-monkee",
			cands: []srrdbSearchResult{
				{Release: "7.Days.in.Hell.2015.1080p.WEB.H264-DiMEPiECE", IsForeign: "no"},
			},
			wantPick: "",
		},
		{
			name:      "known local group match is case-insensitive",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 2015, Group: "monkee", Source: "WEB-DL", Codec: []string{"H.264"}},
			localBase: "Movie 2015 1080p WEB-DL H.264-monkee",
			cands: []srrdbSearchResult{
				{Release: "Movie.2015.1080p.WEB-DL.H.264-MONKEE", IsForeign: "no"},
			},
			wantPick: "Movie.2015.1080p.WEB-DL.H.264-MONKEE",
		},
		{
			name:      "no candidate at the right resolution is not matched",
			release:   api.ReleaseInfo{Resolution: "2160p", Year: 2014, Group: "GRP"},
			localBase: "Movie 2014 2160p BluRay x265 GRP",
			cands: []srrdbSearchResult{
				{Release: "Movie.2014.1080p.BluRay.x264-GRP"},
				{Release: "Movie.2014.720p.BluRay.x264-GRP"},
			},
			wantPick: "",
		},
		{
			name:      "wrong year and group is not matched",
			release:   api.ReleaseInfo{Resolution: "1080p", Year: 2014, Group: "GRP"},
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
			meta := api.PreparedMetadata{Release: tc.release, Tag: tc.tag}
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
				{Name: "driven.2001.1080p.bluray.x264-moovee.nfo", Size: 100},
				{Name: "driven.2001.1080p.bluray.x264-moovee.mkv", Size: 8000000000},
			},
			localMedia:  "moovee-driven.mkv",
			wantRenamed: true,
			wantMatched: true,
		},
		{
			name: "exact case-sensitive filename match is not renamed",
			archived: []srrdbArchivedFile{
				{Name: "driven.2001.1080p.bluray.x264-moovee.mkv", Size: 8000000000},
			},
			localMedia:  "driven.2001.1080p.bluray.x264-moovee.mkv",
			wantRenamed: false,
			wantMatched: true,
		},
		{
			name: "casing-only difference is treated as renamed (srrdb authoritative)",
			archived: []srrdbArchivedFile{
				{Name: "driven.2001.1080p.bluray.x264-moovee.mkv", Size: 8000000000},
			},
			localMedia:  "Driven.2001.1080p.BluRay.x264-MOOVEE.mkv",
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
		132245:  "tt0132245",
		111161:  "tt0111161",
		6946580: "tt6946580",
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
	const folder = "Driven.2001.1080p.BluRay.x264-MOOVEE"
	c := sceneLocalCandidates(api.PreparedMetadata{
		SourcePath: filepath.Join(base, folder),
		VideoPath:  filepath.Join(base, folder, "moovee-driven.mkv"),
	})
	if len(c.folders) != 1 || c.folders[0] != folder {
		t.Fatalf("folder candidates = %v, want [%s]", c.folders, folder)
	}
	if len(c.files) != 1 || c.files[0] != "moovee-driven" {
		t.Fatalf("file candidates = %v, want [moovee-driven]", c.files)
	}
	if c.mediaFilename != "moovee-driven.mkv" {
		t.Fatalf("mediaFilename = %q, want moovee-driven.mkv", c.mediaFilename)
	}

	// Single-file release: SourcePath == VideoPath, no folder candidate.
	singlePath := filepath.Join(base, "movie.2020.1080p.bluray.x264-grp.mkv")
	single := sceneLocalCandidates(api.PreparedMetadata{SourcePath: singlePath, VideoPath: singlePath})
	if len(single.folders) != 0 {
		t.Fatalf("single-file folder candidates = %v, want none", single.folders)
	}
	if len(single.files) != 1 || single.files[0] != "movie.2020.1080p.bluray.x264-grp" {
		t.Fatalf("single-file file candidates = %v", single.files)
	}
}
