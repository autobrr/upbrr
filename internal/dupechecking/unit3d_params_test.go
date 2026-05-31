// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildUnit3DSearchParamsSkipsResolutionForOTW(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "TV"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		ReleaseName: "Show.S01E02.1080p.WEB-DL.H264-GRP",
	}

	params := buildUnit3DSearchParams(meta, "OTW")
	if _, ok := params["resolutions[]"]; ok {
		t.Fatalf("did not expect OTW resolution filter, got %#v", params["resolutions[]"])
	}
	if got := params.Get("name"); got != " S01" {
		t.Fatalf("expected season search name, got %q", got)
	}
}

func TestBuildUnit3DSearchParamsKeepsResolutionForOtherTrackers(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "TV"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		ReleaseName: "Show.S01E02.1080p.WEB-DL.H264-GRP",
	}

	params := buildUnit3DSearchParams(meta, "AITHER")
	if got := params["resolutions[]"]; len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("expected 1080p/1080i search filters, got %#v", got)
	}
}

func TestBuildUnit3DSearchParamsUsesEMUWTrackerMappings(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "MOVIE"},
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "540p"},
		ReleaseName: "Movie.2025.540p.WEB-DL.H264-GRP",
	}

	params := buildUnit3DSearchParams(meta, "EMUW")
	if got := params.Get("name"); got != "" {
		t.Fatalf("expected empty movie search name, got %q", got)
	}
	if got := params.Get("categories[]"); got != "1" {
		t.Fatalf("expected EMUW movie category 1, got %q", got)
	}
	if got := params.Get("types[]"); got != "4" {
		t.Fatalf("expected EMUW WEBDL type 4, got %q", got)
	}
	if got := params.Get("resolutions[]"); got != "7" {
		t.Fatalf("expected EMUW 540p resolution 7, got %q", got)
	}
	if got := params.Get("perPage"); got != "100" {
		t.Fatalf("expected perPage=100, got %q", got)
	}
}

func TestBuildUnit3DSearchParamsUsesEMUWPaired1080Resolution(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{TMDBID: 123, Category: "TV"},
		Type:        "SD",
		Release:     api.ReleaseInfo{Resolution: "1080i"},
		ReleaseName: "Show.S02E03.1080i.HDTV.H264-GRP",
	}

	params := buildUnit3DSearchParams(meta, "EMUW")
	if got := params.Get("name"); got != " S02" {
		t.Fatalf("expected season search name, got %q", got)
	}
	if got := params.Get("categories[]"); got != "2" {
		t.Fatalf("expected EMUW TV category 2, got %q", got)
	}
	if got := params.Get("types[]"); got != "7" {
		t.Fatalf("expected EMUW SD type 7, got %q", got)
	}
	if got := params["resolutions[]"]; len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("expected EMUW 1080p/1080i filters, got %#v", got)
	}
}
