// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameDoesNotAddLanguageMarkersToDiscs(t *testing.T) {
	t.Parallel()

	for _, discType := range []string{"DVD", "BDMV"} {
		t.Run(discType, func(t *testing.T) {
			t.Parallel()
			meta := api.UploadSubject{
				DiscType:    discType,
				Type:        "DISC",
				Source:      "NTSC DVD",
				ReleaseName: "Example Release 2026 480p NTSC DVD DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Language:   []string{"French"},
					Resolution: "480p",
					Year:       2026,
				},
			}
			got := buildName(meta, config.TrackerConfig{})
			if strings.Contains(strings.ToUpper(got), "FRENCH") {
				t.Fatalf("%s disc name added language marker: %q", discType, got)
			}
		})
	}
}
