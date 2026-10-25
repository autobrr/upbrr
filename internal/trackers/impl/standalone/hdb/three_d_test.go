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
