// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestIsInternalGroup(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"MTV": {
					Internal:       true,
					InternalGroups: []string{"GroupA", "GroupB"},
				},
			},
		},
	}

	if !IsInternalGroup(cfg, "MTV", api.UploadSubject{Tag: "-GroupA"}) {
		t.Fatalf("expected group to be internal")
	}
	if IsInternalGroup(cfg, "MTV", api.UploadSubject{Tag: "-Other"}) {
		t.Fatalf("expected group to be non-internal")
	}
	if IsInternalGroup(cfg, "BHD", api.UploadSubject{Tag: "-GroupA"}) {
		t.Fatalf("expected missing tracker to be non-internal")
	}
}
