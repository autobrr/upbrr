// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildUnit3DSearchParamsSkipsResolutionForOTW(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: "TV"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		ReleaseName: "Show.S01E02.1080p.WEB-DL.H264-GRP",
	}

	params := buildDupeSearchParams(meta, "OTW")
	if _, ok := params["resolutions[]"]; ok {
		t.Fatalf("did not expect OTW resolution filter, got %#v", params["resolutions[]"])
	}
	if got := params.Get("name"); got != " S01" {
		t.Fatalf("expected season search name, got %q", got)
	}
}

func TestBuildUnit3DSearchParamsAvoidsPolicyUnsafeNarrowing(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: "TV"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		ReleaseName: "Show.S01E02.1080p.WEB-DL.H264-GRP",
		SeasonInt:   1,
		EpisodeInt:  2,
	}

	params := buildDupeSearchParams(meta, "AITHER")
	if _, ok := params["resolutions[]"]; ok {
		t.Fatalf("did not expect resolution narrowing, got %#v", params["resolutions[]"])
	}
	if _, ok := params["types[]"]; ok {
		t.Fatalf("did not expect type narrowing, got %#v", params["types[]"])
	}
	if got := params.Get("seasonNumber"); got != "1" {
		t.Fatalf("expected season scope, got %q", got)
	}
	if got := params.Get("episodeNumber"); got != "" {
		t.Fatalf("episode narrowing can hide season packs, got %q", got)
	}
}

func TestBuildUnit3DSearchParamsUsesEMUWTrackerMappings(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "540p"},
		ReleaseName: "Movie.2025.540p.WEB-DL.H264-GRP",
	}

	params := buildDupeSearchParams(meta, "EMUW")
	if got := params.Get("name"); got != "" {
		t.Fatalf("expected empty movie search name, got %q", got)
	}
	if got := params.Get("categories[]"); got != "1" {
		t.Fatalf("expected EMUW movie category 1, got %q", got)
	}
	if _, ok := params["types[]"]; ok {
		t.Fatalf("did not expect EMUW type narrowing, got %#v", params["types[]"])
	}
	if _, ok := params["resolutions[]"]; ok {
		t.Fatalf("did not expect EMUW resolution narrowing, got %#v", params["resolutions[]"])
	}
	if got := params.Get("perPage"); got != "100" {
		t.Fatalf("expected perPage=100, got %q", got)
	}
}

func TestBuildUnit3DSearchParamsUsesEMUWPaired1080Resolution(t *testing.T) {
	t.Parallel()

	meta := api.DuplicateSubject{
		Identity:    api.ExternalIdentity{TMDBID: 123, Category: "TV"},
		Type:        "SD",
		Release:     api.ReleaseInfo{Resolution: "1080i"},
		ReleaseName: "Show.S02E03.1080i.HDTV.H264-GRP",
	}

	params := buildDupeSearchParams(meta, "EMUW")
	if got := params.Get("name"); got != " S02" {
		t.Fatalf("expected season search name, got %q", got)
	}
	if got := params.Get("categories[]"); got != "2" {
		t.Fatalf("expected EMUW TV category 2, got %q", got)
	}
	if _, ok := params["types[]"]; ok {
		t.Fatalf("did not expect EMUW type narrowing, got %#v", params["types[]"])
	}
	if _, ok := params["resolutions[]"]; ok {
		t.Fatalf("did not expect EMUW resolution narrowing, got %#v", params["resolutions[]"])
	}
	if got := params.Get("seasonNumber"); got != "" {
		t.Fatalf("expected no canonical season number without prepared coordinates, got %q", got)
	}
}
