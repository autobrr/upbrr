// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

// TestResolveUnit3DTypeIDFollowsCorrectedMediaTypeFact covers the corrected
// canonical fact path: a fact-producing type instruction lands in the prepared
// Media facts, which project into UploadSubject.Type, so the tracker taxonomy
// must map the corrected value ahead of the parsed release type.
func TestResolveUnit3DTypeIDFollowsCorrectedMediaTypeFact(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Type:        "REMUX",
		Release:     api.ReleaseInfo{Type: "ENCODE"},
		ReleaseName: "Example Movie 2027 2160p BluRay REMUX-OTHER",
	}
	typeID, err := resolveUnit3DTypeID(meta)
	if err != nil {
		t.Fatalf("resolve type id: %v", err)
	}
	if typeID != "2" {
		t.Fatalf("type id = %q, want REMUX id %q", typeID, "2")
	}

	parsedOnly := api.UploadSubject{Release: api.ReleaseInfo{Type: "ENCODE"}}
	parsedID, err := resolveUnit3DTypeID(parsedOnly)
	if err != nil {
		t.Fatalf("resolve parsed type id: %v", err)
	}
	if parsedID != "3" {
		t.Fatalf("parsed type id = %q, want ENCODE id %q", parsedID, "3")
	}

	if _, err := resolveUnit3DTypeID(api.UploadSubject{ReleaseName: "Example.Movie.2027.2160p.BluRay.REMUX-GRP"}); err == nil {
		t.Fatal("raw release name unexpectedly supplied a Unit3D type")
	}
}

func TestResolutionUsesResolvedFactOnly(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Release: api.ReleaseInfo{Resolution: "1080p"}, ReleaseName: "Example.Movie.2027.2160p-GRP"}
	if got := Resolution(meta); got != "1080p" {
		t.Fatalf("resolved resolution = %q", got)
	}
	if got := Resolution(api.UploadSubject{ReleaseName: "Example.Movie.2027.2160p-GRP"}); got != "" {
		t.Fatalf("raw-only resolution = %q", got)
	}
	rule := api.RuleSubject{Release: api.ReleaseInfo{Resolution: "720p"}, ReleaseName: "Example.Movie.2027.4320p-GRP"}
	if got := RuleResolution(rule); got != "720p" {
		t.Fatalf("rule resolution = %q", got)
	}
}
