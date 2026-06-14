// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"testing"
)

func TestNewBannedGroupCheckerFromDBPath(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	checker := NewBannedGroupChecker(filepath.Join(tempDir, "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker, got nil")
	}
	bannedDir := filepath.Join(tempDir, "cache", "banned")
	if checker.basePath != bannedDir {
		t.Fatalf("expected base path %q, got %q", bannedDir, checker.basePath)
	}
}
func TestNewBannedGroupCheckerNoPathUsesDefaultRoot(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	checker := NewBannedGroupChecker(" ")
	if checker == nil {
		t.Fatalf("expected checker")
	}
	expected := filepath.Join(home, ".config", "upbrr", "cache", "banned")
	if checker.basePath != expected {
		t.Fatalf("expected base path %q, got %q", expected, checker.basePath)
	}
}

func TestBannedGroupCheckerDPBuiltins(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	for _, group := range []string{"FGT", "PSA", "HorribleSubs", "Subsplease", "SyncUp", "Trix"} {
		banned, err := checker.IsBanned("DP", group)
		if err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		if !banned {
			t.Fatalf("expected %s to be banned on DP", group)
		}
	}
}

func TestBannedGroupCheckerBHDBuiltins(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	for _, group := range []string{"ProRes", "MezRips", "Flights", "BiTOR", "iVy", "QxR", "SyncUP", "OFT", "TGS"} {
		banned, err := checker.IsBanned("BHD", group)
		if err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		if !banned {
			t.Fatalf("expected %s to be banned on BHD", group)
		}
	}
}

func TestBannedGroupCheckerDPDoesNotIncludeRemovedHDT(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	banned, err := checker.IsBanned("DP", "HDT")
	if err != nil {
		t.Fatalf("check HDT: %v", err)
	}
	if banned {
		t.Fatalf("expected HDT not to be banned on DP")
	}
}
