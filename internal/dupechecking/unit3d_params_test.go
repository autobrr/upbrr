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

	params, err := buildUnit3DSearchParams(meta, "OTW")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
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

	params, err := buildUnit3DSearchParams(meta, "AITHER")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := params["resolutions[]"]; len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("expected 1080p/1080i search filters, got %#v", got)
	}
}
