// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestOTWThreeDSubtypePlacement(t *testing.T) {
	subject := api.UploadSubject{
		Is3D:   "3D",
		Type:   "ENCODE",
		Source: "BluRay",
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2026,
			Resolution: "1080p",
		},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature3DHalfOU},
	}
	got := buildName(subject, config.TrackerConfig{})
	if !strings.Contains(got, "1080p 3D Half OU") || strings.Count(got, "3D") != 1 {
		t.Fatal(got)
	}
}
