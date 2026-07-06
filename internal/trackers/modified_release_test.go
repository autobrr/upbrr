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
			Release:    api.ReleaseInfo{Group: "GRP"},
		}
	}

	cases := []struct {
		name string
		meta api.PreparedMetadata
		want bool
	}{
		{
			name: "clean dotted folder with group",
			meta: grouped("/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP"),
			want: false,
		},
		{
			name: "renamed spaced folder with group",
			meta: grouped("/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP"),
			want: true,
		},
		{
			name: "renamed spaced single file with group",
			meta: grouped("/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP.mkv"),
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
				SourcePath: "/data/movies/Example Movie (2026) {imdb-tt1234567}",
				Release:    api.ReleaseInfo{Group: "tt1234567"},
			},
			want: true,
		},
		{
			name: "sonarr-renamed file with tmdb token is flagged",
			meta: api.PreparedMetadata{
				SourcePath: "/data/tv/Example Show (2026) {tmdb-12345}/Example Show S01E01 {tmdb-12345}.mkv",
				VideoPath:  "/data/tv/Example Show (2026) {tmdb-12345}/Example Show S01E01 {tmdb-12345}.mkv",
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
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) {IMDb-tt1234567}"},
			want: true,
		},
		{
			name: "arr token is flagged even when the group tag was stripped",
			// *arr renames frequently drop the trailing -GROUP, so the token check
			// must fire without a parsed group.
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) {tmdb-12345}"},
			want: true,
		},
		{
			name: "tmdb bracket token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) [tmdb-12345]"},
			want: true,
		},
		{
			name: "tmdb paren token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) (tmdb-12345)"},
			want: true,
		},
		{
			name: "imdb bracket token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) [imdb-tt1234567]"},
			want: true,
		},
		{
			name: "imdb paren token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/movies/Example Movie (2026) (imdb-tt1234567)"},
			want: true,
		},
		{
			name: "tvdb bracket token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/tv/Example Show (2026) [tvdb-12345]"},
			want: true,
		},
		{
			name: "tvdb paren token is flagged",
			meta: api.PreparedMetadata{SourcePath: "/data/tv/Example Show (2026) (tvdb-12345)"},
			want: true,
		},
		{
			name: "standard rename check skipped for configured group",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Example Release 2026 2160p MA WEB-DL DDP5 1 HDR H 265-HiDt",
				Release:    api.ReleaseInfo{Group: "HiDt"},
			},
			want: false,
		},
		{
			name: "standard rename check skipped for SPHD",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Example Release 2026 2160p MA WEB-DL DDP5 1 HDR H 265-SPHD",
				Release:    api.ReleaseInfo{Group: "SPHD"},
			},
			want: false,
		},
		{
			name: "standard arr-token rename check skipped for configured group",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Example Release (2026) {imdb-tt1234567}-HiDt",
				Release:    api.ReleaseInfo{Group: "HiDt"},
			},
			want: false,
		},
		{
			name: "standard arr-token rename check skipped for SPHD",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Example Release (2026) {imdb-tt1234567}-SPHD",
				Release:    api.ReleaseInfo{Group: "SPHD"},
			},
			want: false,
		},
		{
			name: "arr-renamed personal release is exempt",
			meta: api.PreparedMetadata{
				SourcePath:      "/data/movies/Example Movie (2026) {imdb-tt1234567}",
				PersonalRelease: true,
			},
			want: false,
		},
		{
			name: "arr-renamed disc source is exempt",
			meta: api.PreparedMetadata{
				SourcePath: "/data/movies/Example Movie (2026) {imdb-tt1234567}",
				DiscType:   "BDMV",
			},
			want: false,
		},
		{
			name: "edition-only brace token without an id token is not an arr rename",
			// Only the {tmdb-/imdb-/tvdb-} id tokens mark an *arr rename; other
			// braces (e.g. {edition-…}) must not trigger the token signal, and the
			// spaces heuristic still skips bracketed names.
			meta: grouped("/data/movies/Example Movie 2026 2160p WEB-DL {edition-Directors Cut}-GRP"),
			want: false,
		},
		{
			name: "parenthesized spaced library name without an id token is not flagged",
			// Guards that the bracket marker still suppresses the whitespace
			// heuristic when no *arr id token is present.
			meta: grouped("/data/movies/Example Movie (2026) 2160p WEB-DL-GRP"),
			want: false,
		},
		{
			name: "personal release is exempt",
			meta: func() api.PreparedMetadata {
				m := grouped("/data/movies/Example Movie 2026 2160p WEB-DL-GRP")
				m.PersonalRelease = true
				return m
			}(),
			want: false,
		},
		{
			name: "disc source is exempt",
			meta: func() api.PreparedMetadata {
				m := grouped("/data/movies/Example Movie 2026 2160p BluRay-GRP")
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
				SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
				VideoPath:  "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP.mkv",
				Release:    api.ReleaseInfo{Group: "GRP"},
			},
			want: true,
		},
		{
			name: "falls back to video path when source path is empty",
			meta: api.PreparedMetadata{
				SourcePath: "",
				VideoPath:  "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP.mkv",
				Release:    api.ReleaseInfo{Group: "GRP"},
			},
			want: true,
		},
		{
			name: "spaced folder retaining dotted tokens is still flagged",
			// A library rename that converts most separators to spaces but leaves
			// dotted tokens (DDP5.1, H.265) must not have its trailing "-GROUP" tag
			// stripped by extension handling on the directory basename (filepath.Ext
			// would otherwise treat ".265-GRP" as an extension).
			meta: grouped("/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5.1 HDR H.265-GRP"),
			want: true,
		},
		{
			name: "srrdb rename signal flags a clean-looking name with no group",
			// The imdb: fallback authoritatively confirmed a rename that the
			// heuristics above cannot see (no group tag, no *arr token, no spaces).
			meta: api.PreparedMetadata{
				SourcePath:         "/data/movies/Example.Movie.2026.1080p.BluRay.x264",
				SceneRenamed:       true,
				SceneRenamedReason: "source appears renamed or modified from its original release name; verify the file hash and source provenance",
			},
			want: true,
		},
		{
			name: "srrdb rename signal still flags configured standard-skip group",
			meta: api.PreparedMetadata{
				SourcePath:   "/data/movies/Example.Release.2026.1080p.BluRay.x264-HiDt",
				Release:      api.ReleaseInfo{Group: "HiDt"},
				SceneRenamed: true,
			},
			want: true,
		},
		{
			name: "srrdb rename signal is exempt for personal release",
			meta: api.PreparedMetadata{
				SourcePath:      "/data/movies/Example.Movie.2026.1080p.BluRay.x264",
				PersonalRelease: true,
				SceneRenamed:    true,
			},
			want: false,
		},
		{
			name: "srrdb rename signal is exempt for disc source",
			meta: api.PreparedMetadata{
				SourcePath:   "/data/movies/Example.Movie.2026.1080p.BluRay.x264",
				DiscType:     "BDMV",
				SceneRenamed: true,
			},
			want: false,
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
