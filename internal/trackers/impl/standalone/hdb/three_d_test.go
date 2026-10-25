// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestGeneratedHDBThreeDPlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:   "3D",
		Type:   "ENCODE",
		Source: "BluRay",
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Year:       2026,
			Resolution: "1080p",
		},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS},
	}
	name := buildGeneratedHDBName(subject, "Example Movie")
	if !strings.Contains(name, "1080p 3D Half SBS") || strings.Count(name, "3D") != 1 {
		t.Fatal(name)
	}
}

func TestGeneratedHDBThreeDPlacementAfterRegion(t *testing.T) {
	for _, test := range []struct{ kind, disc, region, want string }{
		{"DISC", "BDMV", "CEE", "1080p CEE 3D Half SBS Blu-ray"},
		{"REMUX", "", "USA", "1080p USA 3D Half SBS Remux"},
		{"ENCODE", "", "USA", "1080p 3D Half SBS BluRay USA"},
		{"ENCODE", "", "", "1080p 3D Half SBS BluRay"},
	} {
		subject := api.UploadSubject{
			Is3D:     "3D",
			Type:     test.kind,
			DiscType: test.disc,
			Region:   test.region,
			Source:   "BluRay",
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Year:       2026,
				Resolution: "1080p",
			},
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS},
		}
		if got := buildGeneratedHDBName(subject, "Example Movie"); got != "Example Movie 2026 "+test.want {
			t.Fatalf("%s name = %q, want tail %q", test.kind, got, test.want)
		}
	}
}
