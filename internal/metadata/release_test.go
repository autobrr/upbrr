// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import "testing"

func TestParseReleaseInfoMovieUsesRLSCategoryAndSource(t *testing.T) {
	release := ParseReleaseInfo("Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv")

	if release.Category != "MOVIE" {
		t.Fatalf("expected MOVIE category, got %q", release.Category)
	}
	if release.Type != "WEB-DL" {
		t.Fatalf("expected WEB-DL type, got %q", release.Type)
	}
}

func TestParseReleaseInfoEpisodeUsesTVCategoryAndSourceAsType(t *testing.T) {
	release := ParseReleaseInfo("Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv")

	if release.Category != "TV" {
		t.Fatalf("expected TV category, got %q", release.Category)
	}
	if release.Type != "WEB-DL" {
		t.Fatalf("expected WEB-DL type, got %q", release.Type)
	}
}

func TestParseReleaseInfoSeasonPackUsesTVCategory(t *testing.T) {
	release := ParseReleaseInfo("Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP")

	if release.Category != "TV" {
		t.Fatalf("expected TV category, got %q", release.Category)
	}
	if release.Type != "WEB-DL" {
		t.Fatalf("expected WEB-DL type, got %q", release.Type)
	}
}
