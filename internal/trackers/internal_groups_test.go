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
				"BTN": {
					Internal:       false,
					InternalGroups: config.CSVList{"GroupA", "GroupB"},
				},
			},
		},
	}

	if !IsInternalGroup(cfg, "BTN", api.UploadSubject{Tag: "-GroupA"}) {
		t.Fatalf("expected group to be internal")
	}
	if IsInternalGroup(cfg, "BTN", api.UploadSubject{Tag: "-Other"}) {
		t.Fatalf("expected group to be non-internal")
	}
	if IsInternalGroup(cfg, "BHD", api.UploadSubject{Tag: "-GroupA"}) {
		t.Fatalf("expected missing tracker to be non-internal")
	}
}

func TestResolveGroupPolicyKeepsTrackerListsIndependent(t *testing.T) {
	t.Parallel()

	trackerCfg := config.TrackerConfig{
		DupeBypassGroups:      config.CSVList{"Dupe"},
		PersonalReleaseGroups: config.CSVList{"Personal"},
		InternalGroups:        config.CSVList{"Internal"},
	}
	tests := []struct {
		name     string
		tag      string
		personal bool
		internal bool
		sameOnly bool
	}{
		{
			name:     "dupe",
			tag:      "-dupe",
			sameOnly: true,
		},
		{
			name:     "personal",
			tag:      "Personal",
			personal: true,
		},
		{
			name:     "internal",
			tag:      "-INTERNAL",
			internal: true,
			sameOnly: true,
		},
		{name: "sentinel", tag: "-unknown"},
		{name: "substring", tag: "DupeExtra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ResolveGroupPolicy(trackerCfg, api.UploadSubject{Tag: tt.tag})
			if decision.PersonalRelease != tt.personal || decision.Internal != tt.internal || decision.SameGroupOnly != tt.sameOnly {
				t.Fatalf("ResolveGroupPolicy() = %#v", decision)
			}
		})
	}
}

func TestResolveGroupPolicyExplicitPersonalReleaseWins(t *testing.T) {
	t.Parallel()

	no := false
	yes := true
	trackerCfg := config.TrackerConfig{PersonalReleaseGroups: config.CSVList{"Mine"}}
	if got := ResolveGroupPolicy(trackerCfg, api.UploadSubject{Tag: "-Mine", PersonalReleaseOverride: &no}); got.PersonalRelease {
		t.Fatalf("explicit false did not override group default")
	}
	if got := ResolveGroupPolicy(config.TrackerConfig{}, api.UploadSubject{Tag: "-Other", PersonalReleaseOverride: &yes}); !got.PersonalRelease {
		t.Fatalf("explicit true was not preserved")
	}
}

func TestNormalizeTrackerReleaseGroupRejectsAmbiguousValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"NTb Other", "NTb/Other", "NTb,Other", "NTb|Other", "NTb&Other", "NTb+Other"} {
		if got := NormalizeTrackerReleaseGroup(value); got != "" {
			t.Errorf("NormalizeTrackerReleaseGroup(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"-NTb", "A.B", "A_B", "A-B"} {
		if got := NormalizeTrackerReleaseGroup(value); got == "" {
			t.Errorf("NormalizeTrackerReleaseGroup(%q) rejected punctuation", value)
		}
	}
}
