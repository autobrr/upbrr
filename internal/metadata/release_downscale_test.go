// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"testing"
)

func TestParseReleaseInfoPreservesExplicitDownscaleEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		want  bool
	}{
		{"web technical token", "Example.Series.S01.1080p.DS4K.DSNP.WEBRip.DDP5.1.x265-GRP.mkv", true},
		{"broadcast technical token", "Example.Series.S01E01.1080p.DS4K.HDTV.x264-GRP.mkv", true},
		{"lowercase technical token", "Example.Movie.2026.1080p.ds4k.WEBRip.x264-GRP.mkv", true},
		{"no source evidence", "Example.Series.S01.1080p.DSNP.WEBRip.DDP5.1.x265-GRP.mkv", false},
		{"movie title", "DS4K.2026.1080p.WEBRip.x264-GRP.mkv", false},
		{"episode title", "Example.Series.S01E01.DS4K.1080p.WEBRip.x264-GRP.mkv", false},
		{"release group", "Example.Series.S01.1080p.WEBRip.x264-DS4K.mkv", false},
		{"unsupported marker", "Example.Series.S01.1080p.DS8K.WEBRip.x264-GRP.mkv", false},
		{"token boundary", "Example.Series.S01.1080p.NOTDS4K.WEBRip.x264-GRP.mkv", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ParseReleaseInfo(test.input)
			if present := slices.Contains(got.Other, "DS4K"); present != test.want {
				t.Fatalf("DS4K evidence = %t, want %t; other = %q", present, test.want, got.Other)
			}
		})
	}
}

func TestDownscaleEvidencePreservesGenuineEpisodeTitle(t *testing.T) {
	for _, test := range []struct{ input, subtitle string }{
		{"Example.Series.S01E01.DS4K.1080p.DS4K.WEBRip.x264-GRP.mkv", "DS4K"},
		{"Example.Series.S01E01.1080p.DS4K.WEBRip.x264-GRP.mkv", ""},
	} {
		got := ParseReleaseInfo(test.input)
		if got.Subtitle != test.subtitle || !slices.Contains(got.Other, "DS4K") {
			t.Fatalf("subtitle=%q other=%q; want subtitle=%q with marker", got.Subtitle, got.Other, test.subtitle)
		}
	}
}
