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
}
