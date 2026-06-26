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
			name: "plex/radarr library folder with id token is not flagged",
			// rls mis-parses "tt2713180" as the group; the bracket marker + suffix
			// guard must keep this from being treated as a rename.
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Fury (2014) {imdb-tt2713180}",
				Release:    api.ReleaseInfo{Group: "tt2713180"},
			},
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
