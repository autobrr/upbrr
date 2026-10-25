// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestRHDThreeDSourcePlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:            "3D",
		Type:            "DISC",
		DiscType:        "BDMV",
		Region:          "CEE",
		Release:         api.ReleaseInfo{Source: "Blu-ray"},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DFullSBS},
	}
	got := strings.Join(typeAndSource(subject), " ")
	if !strings.Contains(got, "CEE 3D Full SBS Blu-ray") || strings.Count(got, "3D") != 1 {
		t.Fatal(got)
	}
}

func TestRHDThreeDSourcePlacementOtherTypes(t *testing.T) {
	for _, kind := range []string{"HDTV", "DVDRIP", ""} {
		subject := api.UploadSubject{Is3D: "3D", Type: kind}
		got := strings.Join(typeAndSource(subject), " ")
		want := strings.TrimSpace("3D " + kind)
		if got != want {
			t.Fatalf("%q: got %q, want %q", kind, got, want)
		}
	}
}
