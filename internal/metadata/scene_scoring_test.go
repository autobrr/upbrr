// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
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

func TestCanonicalMediaBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		archived  []srrdbArchivedFile
		localBase string
		want      string
	}{
		{
			name: "renamed local basename returns canonical media name",
			archived: []srrdbArchivedFile{
				{Name: "fury.2014.1080p.bluray.x264-grp.nfo", Size: 100},
				{Name: "fury.2014.1080p.bluray.x264-grp.mkv", Size: 8000000000},
			},
			localBase: "Fury 2014 1080p BluRay x264 GRP",
			want:      "fury.2014.1080p.bluray.x264-grp",
		},
		{
			name: "matching basename (case aside) is not a rename",
			archived: []srrdbArchivedFile{
				{Name: "Fury.2014.1080p.BluRay.x264-GRP.mkv", Size: 8000000000},
			},
			localBase: "fury.2014.1080p.bluray.x264-grp",
			want:      "",
		},
		{
			name: "season pack: local episode matching a canonical file is not a rename",
			archived: []srrdbArchivedFile{
				{Name: "show.s01e01.1080p.web-dl-grp.mkv", Size: 2000000000},
				{Name: "show.s01e02.1080p.web-dl-grp.mkv", Size: 2100000000},
			},
			localBase: "Show.S01E02.1080p.WEB-DL-GRP",
			want:      "",
		},
		{
			name: "no media files yields no verdict",
			archived: []srrdbArchivedFile{
				{Name: "release.nfo", Size: 100},
				{Name: "sample/something.txt", Size: 10},
			},
			localBase: "Whatever",
			want:      "",
		},
		{
			name:      "largest media file is chosen as representative",
			localBase: "renamed name",
			archived: []srrdbArchivedFile{
				{Name: "movie-sample.mkv", Size: 50000000},
				{Name: "movie.2014.1080p.bluray.x264-grp.mkv", Size: 9000000000},
			},
			want: "movie.2014.1080p.bluray.x264-grp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := canonicalMediaBase(tc.archived, tc.localBase)
			if got != tc.want {
				t.Fatalf("canonicalMediaBase = %q, want %q", got, tc.want)
			}
		})
	}
}
