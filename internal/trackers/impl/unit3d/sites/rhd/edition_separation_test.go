// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestHybridFeatureSurvivesCustomName(t *testing.T) {
	for _, set := range []string{"", "2in1"} {
		subject := api.UploadSubject{
			EditionSet:      set,
			Cut:             "Director's Cut",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid},
			Release:         api.ReleaseInfo{Title: "Example Movie", Year: 2026},
		}
		name := buildName(subject, config.TrackerConfig{})
		if strings.Count(name, "Hybrid") != 1 {
			t.Fatalf("name=%q", name)
		}
		if set == "" && !strings.Contains(name, "Director's Cut") || set != "" && !strings.Contains(name, set) {
			t.Fatalf("edition missing from %q", name)
		}
	}
}
