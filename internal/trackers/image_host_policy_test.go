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
			cfg:     config.TrackerConfig{ImageHost: "ptpimg"},
			overrides: api.ImageHostOverrides{
				PreferredHost: &preferredImgBB,
			},
			wantPreferred:    "ptpimg",
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
			wantAllowedCount: 1,
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
					if len(policy.allowed) != tc.wantAllowedCount || (tc.wantAllowedCount == 1 && policy.allowed[0] != "imgbox") {
						t.Fatalf("expected configured no-policy tracker host to be required, got %#v", policy)
					}
				}
			}
		})
	}
}
