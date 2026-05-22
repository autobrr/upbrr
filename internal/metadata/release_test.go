// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import "testing"

func TestParseReleaseInfo(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		category string
		typ      string
		source   string
	}{
		{
			name:     "empty input returns defaults",
			input:    "",
			category: "",
			typ:      "",
			source:   "",
		},
		{
			name:     "malformed filename leaves derived fields empty",
			input:    "invalid_filename",
			category: "",
			typ:      "",
			source:   "",
		},
		{
			name:     "movie uses rls category and source",
			input:    "Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBDL",
			source:   "Web",
		},
		{
			name:     "episode uses tv category and webdl source",
			input:    "Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
			category: "TV",
			typ:      "WEBDL",
			source:   "Web",
		},
		{
			name:     "season pack uses tv category",
			input:    "Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP",
			category: "TV",
			typ:      "WEBDL",
			source:   "Web",
		},
		{
			name:     "bare web filename uses webdl before webrip",
			input:    "Movie.2026.2160p.WEB.DDP5.1.H.265-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBDL",
			source:   "Web",
		},
		{
			name:     "webrip filename uses webrip",
			input:    "Movie.2026.2160p.WEBRip.DDP5.1.H.265-GRP.mkv",
			category: "MOVIE",
			typ:      "WEBRIP",
			source:   "Web",
		},
		{
			name:     "bluray remux preserves distinct source and type",
			input:    "Movie.2026.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv",
			category: "MOVIE",
			typ:      "REMUX",
			source:   "BluRay",
		},
		{
			name:     "bluray encode infers encode type",
			input:    "Movie.2026.1080p.BluRay.x264-GRP.mkv",
			category: "MOVIE",
			typ:      "ENCODE",
			source:   "BluRay",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			release := ParseReleaseInfo(tc.input)

			if release.Category != tc.category {
				t.Errorf("expected category %q, got %q", tc.category, release.Category)
			}
			if release.Type != tc.typ {
				t.Errorf("expected type %q, got %q", tc.typ, release.Type)
			}
			if release.Source != tc.source {
				t.Errorf("expected source %q, got %q", tc.source, release.Source)
			}
		})
	}
}
