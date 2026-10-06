// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestProfileIdentityAndCapabilities(t *testing.T) {
	t.Parallel()

	profile := Profile()
	if profile.Name != "FLD" {
		t.Fatalf("expected Name FLD, got %q", profile.Name)
	}
	if profile.BaseURL != "https://flood.st" {
		t.Fatalf("expected BaseURL https://flood.st, got %q", profile.BaseURL)
	}
	if profile.DescriptionGroup != "fld" {
		t.Fatalf("expected DescriptionGroup fld, got %q", profile.DescriptionGroup)
	}
	if profile.UploadContentMode != trackers.UploadContentModeDescription {
		t.Fatalf("expected UploadContentMode description, got %v", profile.UploadContentMode)
	}
	if profile.AuthCapability == nil || !profile.AuthCapability.RequiresAPIKey {
		t.Fatalf("expected API key auth capability, got %#v", profile.AuthCapability)
	}
	if profile.UploadArtifactPolicy == nil || profile.UploadArtifactPolicy.Source != "FLD" {
		t.Fatalf("expected UploadArtifactPolicy source FLD, got %#v", profile.UploadArtifactPolicy)
	}
	if profile.ReleaseNamePolicy.ID != "standalone/fld/v1" {
		t.Fatalf("expected release name policy ID standalone/fld/v1, got %q", profile.ReleaseNamePolicy.ID)
	}
	if profile.ValidationPolicy.ID != "standalone-fld-constructibility-v1" {
		t.Fatalf("expected validation policy ID standalone-fld-constructibility-v1, got %q", profile.ValidationPolicy.ID)
	}
	if len(profile.BannedGroups) == 0 || !slices.Contains(profile.BannedGroups, "YIFY") {
		t.Fatalf("expected BannedGroups to contain YIFY, got %#v", profile.BannedGroups)
	}
	if profile.TorrentIdentityPolicy == nil || len(profile.TorrentIdentityPolicy.TrackerURLPatterns) == 0 {
		t.Fatalf("expected TorrentIdentityPolicy with tracker url patterns, got %#v", profile.TorrentIdentityPolicy)
	}
}

func TestDefinitionCapabilities(t *testing.T) {
	t.Parallel()

	def := New()
	if def.Name() != "FLD" {
		t.Fatalf("expected def Name FLD, got %q", def.Name())
	}
	if def.TrackerFamily() != trackers.FamilyStandalone {
		t.Fatalf("expected def Family standalone, got %v", def.TrackerFamily())
	}
	if def.UploadContentMode() != trackers.UploadContentModeDescription {
		t.Fatalf("expected def UploadContentMode description, got %v", def.UploadContentMode())
	}
	if def.DescriptionGroup() != "fld" {
		t.Fatalf("expected def DescriptionGroup fld, got %q", def.DescriptionGroup())
	}
	if def.ReleaseNamePolicy().ID != "standalone/fld/v1" {
		t.Fatalf("expected def release name policy ID standalone/fld/v1, got %q", def.ReleaseNamePolicy().ID)
	}
	if def.ValidationPolicy().ID != "standalone-fld-constructibility-v1" {
		t.Fatalf("expected def validation policy ID standalone-fld-constructibility-v1, got %q", def.ValidationPolicy().ID)
	}
	banned := def.BannedGroups()
	if !slices.Contains(banned, "YIFY") || !slices.Contains(banned, "4K4U") || !slices.Contains(banned, "RARBG") {
		t.Fatalf("expected banned groups to include known groups, got %#v", banned)
	}
}

func TestBannedGroupsPolicy(t *testing.T) {
	t.Parallel()

	knownBanned := []string{"4K4U", "AOC", "C4K", "CRUCiBLE", "d3g", "EASports", "FGT", "MeGusta", "MezRips", "nikt0", "ProRes", "RARBG", "ReaLHD", "SasukeducK", "Sicario", "TEKNO3D", "Telly", "tigole", "TOMMY", "WKS", "x0r", "YIFY"}
	for _, group := range knownBanned {
		if !isBannedReleaseGroup(group) {
			t.Errorf("expected group %s to be banned", group)
		}
	}
	for _, allowed := range []string{"FraMeSToR", "FLUX", "GRP", "playBD"} {
		if isBannedReleaseGroup(allowed) {
			t.Errorf("expected group %s to NOT be banned", allowed)
		}
	}
}
