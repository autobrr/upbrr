// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import "testing"

func TestParseReleaseInfoMovieUsesRLSCategoryAndSource(t *testing.T) {
	release := ParseReleaseInfo("Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv")

	if release.Category != "MOVIE" {
		t.Fatalf("expected MOVIE category, got %q", release.Category)
	}
	if release.Type != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", release.Type)
	}
	if release.Source != "Web" {
		t.Fatalf("expected Web source, got %q", release.Source)
	}
}

func TestParseReleaseInfoEpisodeUsesTVCategoryAndSourceAsType(t *testing.T) {
	release := ParseReleaseInfo("Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv")

	if release.Category != "TV" {
		t.Fatalf("expected TV category, got %q", release.Category)
	}
	if release.Type != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", release.Type)
	}
	if release.Source != "Web" {
		t.Fatalf("expected Web source, got %q", release.Source)
	}
}

func TestParseReleaseInfoSeasonPackUsesTVCategory(t *testing.T) {
	release := ParseReleaseInfo("Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP")

	if release.Category != "TV" {
		t.Fatalf("expected TV category, got %q", release.Category)
	}
	if release.Type != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", release.Type)
	}
	if release.Source != "Web" {
		t.Fatalf("expected Web source, got %q", release.Source)
	}
}

func TestParseReleaseInfoBlurayRemuxPreservesDistinctSourceAndType(t *testing.T) {
	release := ParseReleaseInfo("Movie.2026.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv")

	if release.Source != "BluRay" {
		t.Fatalf("expected BluRay source, got %q", release.Source)
	}
	if release.Type != "REMUX" {
		t.Fatalf("expected REMUX type, got %q", release.Type)
	}
}

func TestParseReleaseInfoBlurayEncodeInfersEncodeType(t *testing.T) {
	release := ParseReleaseInfo("Movie.2026.1080p.BluRay.x264-GRP.mkv")

	if release.Source != "BluRay" {
		t.Fatalf("expected BluRay source, got %q", release.Source)
	}
	if release.Type != "ENCODE" {
		t.Fatalf("expected ENCODE type, got %q", release.Type)
	}
}
