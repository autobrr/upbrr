// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ras

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

func TestBannedGroups(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	checker := trackers.NewBannedGroupCheckerWithRegistry(t.TempDir(), registry)
	for _, group := range []string{"YTS", "yify", "LAMA", "megusta", "NAHOM", "GalaxyRG", "RARBG", "INFINITY"} {
		banned, err := checker.IsBanned("RAS", group)
		if err != nil || !banned {
			t.Fatalf("%s: banned=%t err=%v", group, banned, err)
		}
	}
	for _, group := range []string{"GRP", "YTSOther"} {
		banned, err := checker.IsBanned("RAS", group)
		if err != nil || banned {
			t.Fatalf("%s: banned=%t err=%v", group, banned, err)
		}
	}
}
