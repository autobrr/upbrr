// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestIsRenamedRelease(t *testing.T) {
	t.Parallel()

	grouped := func(sourcePath string) api.PreparedMetadata {
		return api.PreparedMetadata{
			SourcePath: sourcePath,
			Release:    api.ReleaseInfo{Group: "HHWEB"},
		}
	}

	cases := []struct {
		name string
		meta api.PreparedMetadata
		want bool
	}{
		{
			name: "clean dotted folder with group",
			meta: grouped("/data/movies/Fury.2014.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-HHWEB"),
			want: false,
		},
		{
			name: "renamed spaced folder with group",
			meta: grouped("/data/movies/Fury 2014 2160p MA WEB-DL DDP5 1 HDR H 265-HHWEB"),
			want: true,
		},
		{
			name: "renamed spaced single file with group",
			meta: grouped("/data/movies/Fury 2014 2160p MA WEB-DL DDP5 1 HDR H 265-HHWEB.mkv"),
			want: true,
		},
		{
			name: "spaced name without group tag is not flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Some Home Video 2024"},
			want: false,
		},
		{
			name: "spaced name whose group is not the trailing tag is not flagged",
			// Guards against a parser mis-extracting a token as the group.
			meta: grouped("/data/movies/Some Renamed Movie 2024 1080p WEB-DL"),
			want: false,
		},
		{
			name: "radarr-renamed folder with imdb token is flagged",
			// *arr injects {imdb-…} when it renames; this is a modified release the
			// tracker rejects, regardless of how rls parses the group.
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Fury (2014) {imdb-tt2713180}",
				Release:    api.ReleaseInfo{Group: "tt2713180"},
			},
			want: true,
		},
		{
			name: "sonarr-renamed file with tmdb token is flagged",
			meta: api.PreparedMetadata{
				SourcePath: "/data/tv/Severance (2022) {tmdb-95396}/Severance S01E01 {tmdb-95396}.mkv",
				VideoPath:  "/data/tv/Severance (2022) {tmdb-95396}/Severance S01E01 {tmdb-95396}.mkv",
			},
			want: true,
		},
		{
			name: "tvdb token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/tv/Show (2020) {tvdb-360893}"},
			want: true,
		},
		{
			name: "arr token match is case-insensitive",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Fury (2014) {IMDb-tt2713180}"},
			want: true,
		},
		{
			name: "arr token is flagged even when the group tag was stripped",
			// *arr renames frequently drop the trailing -GROUP, so the token check
			// must fire without a parsed group.
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Fury (2014) {tmdb-99861}"},
			want: true,
		},
		{
			name: "arr-renamed personal release is exempt",
			meta: api.PreparedMetadata{
				SourcePath:      "/data/movies/Fury (2014) {imdb-tt2713180}",
				PersonalRelease: true,
			},
			want: false,
		},
		{
			name: "arr-renamed disc source is exempt",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Fury (2014) {imdb-tt2713180}",
				DiscType:   "BDMV",
			},
			want: false,
		},
		{
			name: "edition-only brace token without an id token is not an arr rename",
			// Only the {tmdb-/imdb-/tvdb-} id tokens mark an *arr rename; other
			// braces (e.g. {edition-…}) must not trigger the token signal, and the
			// spaces heuristic still skips bracketed names.
			meta: grouped("/data/movies/Fury 2014 2160p WEB-DL {edition-Directors Cut}-HHWEB"),
			want: false,
		},
		{
			name: "parenthesized spaced library name without an id token is not flagged",
			// Guards that the bracket marker still suppresses the whitespace
			// heuristic when no *arr id token is present.
			meta: grouped("/data/movies/Fury (2014) 2160p WEB-DL-HHWEB"),
			want: false,
		},
		{
			name: "personal release is exempt",
			meta: func() api.PreparedMetadata {
				m := grouped("/data/movies/Fury 2014 2160p WEB-DL-HHWEB")
				m.PersonalRelease = true
				return m
			}(),
			want: false,
		},
		{
			name: "disc source is exempt",
			meta: func() api.PreparedMetadata {
				m := grouped("/data/movies/Fury 2014 2160p BluRay-HHWEB")
				m.DiscType = "BDMV"
				return m
			}(),
			want: false,
		},
		{
			name: "clean folder containing a spaced video file is flagged",
			// Finding: the tracker inspects the file, so a spaced file inside a clean
			// dotted folder must still be detected.
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Fury.2014.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-HHWEB",
				VideoPath:  "/data/movies/Fury.2014.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-HHWEB/Fury 2014 2160p MA WEB-DL DDP5 1 HDR H 265-HHWEB.mkv",
				Release:    api.ReleaseInfo{Group: "HHWEB"},
			},
			want: true,
		},
		{
			name: "falls back to video path when source path is empty",
			meta: api.PreparedMetadata{
				SourcePath: "",
				VideoPath:  "/data/movies/Fury 2014 2160p MA WEB-DL DDP5 1 HDR H 265-HHWEB.mkv",
				Release:    api.ReleaseInfo{Group: "HHWEB"},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reason := isRenamedRelease(tc.meta)
			if got != tc.want {
				t.Fatalf("isRenamedRelease = %v (%q), want %v", got, reason, tc.want)
			}
			if got && reason == "" {
				t.Fatal("expected a non-empty reason when renamed")
			}
		})
	}
}
