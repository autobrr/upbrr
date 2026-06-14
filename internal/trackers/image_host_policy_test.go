// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveImageHostPolicy(t *testing.T) {
	t.Parallel()

	preferredImgBB := "imgbb"
	preferredHDB := "hdb"
	tests := []struct {
		name             string
		tracker          string
		cfg              config.TrackerConfig
		overrides        api.ImageHostOverrides
		wantPreferred    string
		wantAllowedCount int
		wantErr          bool
	}{
		{
			name:    "tracker host prefers over cli host",
			tracker: "OE",
			cfg:     config.TrackerConfig{ImageHost: "imgbox"},
			overrides: api.ImageHostOverrides{
				PreferredHost: &preferredImgBB,
			},
			wantPreferred:    "imgbox",
			wantAllowedCount: -1,
		},
		{
			name:    "cli host applies when tracker host empty",
			tracker: "OE",
			cfg:     config.TrackerConfig{},
			overrides: api.ImageHostOverrides{
				PreferredHost: &preferredImgBB,
			},
			wantPreferred:    "imgbb",
			wantAllowedCount: -1,
		},
		{
			name:             "configured host for no policy tracker",
			tracker:          "AITHER",
			cfg:              config.TrackerConfig{ImageHost: "imgbox"},
			overrides:        api.ImageHostOverrides{},
			wantPreferred:    "imgbox",
			wantAllowedCount: 0,
		},
		{
			name:             "rejects owned cli host for other tracker",
			tracker:          "AITHER",
			cfg:              config.TrackerConfig{},
			overrides:        api.ImageHostOverrides{PreferredHost: &preferredHDB},
			wantAllowedCount: -1,
			wantErr:          true,
		},
		{
			name:             "allows owned cli host for owner",
			tracker:          "HDB",
			cfg:              config.TrackerConfig{},
			overrides:        api.ImageHostOverrides{PreferredHost: &preferredHDB},
			wantPreferred:    "hdb",
			wantAllowedCount: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy, err := resolveImageHostPolicy(tc.tracker, tc.cfg, tc.overrides)
			if (err != nil) != tc.wantErr {
				if tc.wantErr {
					t.Fatal("expected owned host override to fail for other tracker")
				}
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				return
			}

			if tc.wantPreferred != "" {
				got := preferredHost(policy)
				switch tc.name {
				case "tracker host prefers over cli host":
					if got != tc.wantPreferred {
						t.Fatalf("expected tracker image_host to win, got %q", got)
					}
				case "cli host applies when tracker host empty":
					if got != tc.wantPreferred {
						t.Fatalf("expected CLI image host to be preferred, got %q", got)
					}
				case "configured host for no policy tracker":
					if got != tc.wantPreferred {
						t.Fatalf("expected configured host imgbox, got %q", got)
					}
				case "allows owned cli host for owner":
					if got != tc.wantPreferred {
						t.Fatalf("expected owned host for owner tracker, got %q", got)
					}
				}
			}

			switch tc.name {
			case "tracker host prefers over cli host":
				if len(policy.allowed) <= 1 {
					t.Fatalf("expected tracker image_host to preserve fallback hosts, got %#v", policy)
				}
			case "cli host applies when tracker host empty":
				if len(policy.allowed) <= 1 {
					t.Fatalf("expected CLI override to preserve allowed fallback hosts, got %#v", policy)
				}
			default:
				if tc.wantAllowedCount >= 0 {
					if len(policy.allowed) != tc.wantAllowedCount {
						t.Fatalf("expected allowed host count %d, got %#v", tc.wantAllowedCount, policy)
					}
				}
			}
		})
	}
}

func TestNeededImageUploadTargetsFallsBackFromTrackerConfiguredHostForUnrestrictedTracker(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargetsExcluding(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"HHD": {ImageHost: "pixhost"},
			},
		},
	}, []string{"HHD"}, "pixhost", []string{"pixhost"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 || targets[0].Host != "imgbb" {
		t.Fatalf("expected unrestricted tracker to fall back to global imgbb, got %#v", targets)
	}
}

func TestNeededImageUploadTargetsDoesNotFallbackToUnsupportedHostForRestrictedTracker(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargetsExcluding(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "imgbox",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"PTP": {ImageHost: "pixhost"},
			},
		},
	}, []string{"PTP"}, "pixhost", []string{"pixhost"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected restricted tracker to reject unsupported imgbox fallback, got %#v", targets)
	}
}

func TestNeededImageUploadTargetsChoosesSharedApprovedHost(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
			Host2: "imgbox",
		},
	}, []string{"STC", "GPW"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one shared target, got %#v", targets)
	}
	if targets[0].Host != "imgbox" {
		t.Fatalf("expected imgbox to satisfy both trackers, got %#v", targets[0])
	}
	if got, want := targets[0].Trackers, []string{"STC", "GPW"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected trackers %v, got %v", want, got)
	}
}

func TestNeededImageUploadTargetsUsesConfiguredHostPriority(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "pixhost",
			Host2: "imgbox",
			Host3: "imgbb",
		},
	}, []string{"OE"}, "pixhost")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %#v", targets)
	}
	if targets[0].Host != "imgbox" {
		t.Fatalf("expected first configured OE-approved host imgbox, got %#v", targets[0])
	}
}

func TestNeededImageUploadTargetsDoesNotUseUnconfiguredPolicyHost(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
	}, []string{"PTP"}, "imgbox")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one configured approved host, got %#v", targets)
	}
	if targets[0].Host != "imgbb" {
		t.Fatalf("expected configured approved host imgbb instead of unconfigured policy host, got %#v", targets[0])
	}
}

func TestCandidateImageUploadTargetHostsUsesTrackerPolicyPreference(t *testing.T) {
	t.Parallel()

	policy := policyForTracker("PTP", config.TrackerConfig{})
	hosts := candidateImageUploadTargetHosts("PTP", policy, []string{"imgbb", "pixhost"}, map[string]struct{}{})
	if got, want := len(hosts), 2; got != want {
		t.Fatalf("expected %d hosts, got %#v", want, hosts)
	}
	if hosts[0] != "pixhost" || hosts[1] != "imgbb" {
		t.Fatalf("expected tracker policy order [pixhost imgbb], got %#v", hosts)
	}
}

func TestNeededImageUploadTargetsAllowsTrackerConfiguredHost(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"OE": {ImageHost: "imgbox"},
			},
		},
	}, []string{"OE"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 || targets[0].Host != "imgbox" {
		t.Fatalf("expected tracker configured image host imgbox, got %#v", targets)
	}
}

func TestNeededImageUploadTargetsDoesNotShareTrackerConfiguredHostUnlessGloballyConfigured(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"OE":  {ImageHost: "imgbox"},
				"STC": {},
			},
		},
	}, []string{"OE", "STC"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected direct and global targets, got %#v", targets)
	}
	if targets[0].Host != "imgbox" || len(targets[0].Trackers) != 1 || targets[0].Trackers[0] != "OE" {
		t.Fatalf("expected imgbox only for OE, got %#v", targets[0])
	}
	if targets[1].Host != "imgbb" || len(targets[1].Trackers) != 1 || targets[1].Trackers[0] != "STC" {
		t.Fatalf("expected imgbb only for STC, got %#v", targets[1])
	}
}

func TestNeededImageUploadTargetsUsesConfiguredLostimgForLST(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1:          "imgbb",
			LostimgEnabled: true,
			LostimgAPI:     "secret",
		},
	}, []string{"LST"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %#v", targets)
	}
	if targets[0].Host != "lostimg" || targets[0].UsageScope != "tracker:LST" {
		t.Fatalf("expected LST scoped lostimg target, got %#v", targets[0])
	}
}

func TestNeededImageUploadTargetsSkipsLostimgWhenDisabled(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
	}, []string{"LST"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 || targets[0].Host != "imgbb" {
		t.Fatalf("expected global imgbb fallback, got %#v", targets)
	}
}

func TestNeededImageUploadTargetsUsesConfiguredReelflixForRF(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"RF": {ImageHost: "reelflix", ImgAPI: "secret"},
			},
		},
	}, []string{"RF"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %#v", targets)
	}
	if targets[0].Host != "reelflix" || targets[0].UsageScope != "tracker:RF" {
		t.Fatalf("expected RF scoped reelflix target, got %#v", targets[0])
	}
}

func TestNeededImageUploadTargetsSkipsReelflixWhenDisabled(t *testing.T) {
	t.Parallel()

	targets, err := NeededImageUploadTargets(config.Config{
		ImageHosting: config.ImageHostingConfig{
			Host1: "imgbb",
		},
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"RF": {ImgAPI: "secret"},
			},
		},
	}, []string{"RF"}, "imgbb")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(targets) != 1 || targets[0].Host != "imgbb" {
		t.Fatalf("expected global imgbb fallback, got %#v", targets)
	}
}
