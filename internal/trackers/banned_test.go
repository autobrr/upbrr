// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"os"
	"path/filepath"
	"strings"
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
	expected := filepath.Join(home, ".upbrr", "cache", "banned")
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

func TestBannedGroupCheckerBTNBuiltins(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	for _, group := range []string{"FGT", "GalaxyTV", "MeGusta", "[Oj]"} {
		banned, err := checker.IsBanned("BTN", group)
		if err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		if !banned {
			t.Fatalf("expected %s to be banned on BTN", group)
		}
	}
}

func TestBannedGroupCheckerMergesBuiltinsWithCacheFile(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker")
	}
	if err := os.MkdirAll(checker.basePath, 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	filePath := filepath.Join(checker.basePath, "RHD_banned_groups.json")
	if err := os.WriteFile(filePath, []byte(`{"banned_groups":"CustomRHD, Another.Custom"}`), 0o600); err != nil {
		t.Fatalf("write banned groups: %v", err)
	}

	for _, group := range []string{"MagicX", "CustomRHD", "another.custom"} {
		banned, err := checker.IsBanned("RHD", group)
		if err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		if !banned {
			t.Fatalf("expected %s to be banned on RHD", group)
		}
	}
}

func TestBannedGroupCheckerUnreadableCacheFallsBackToRhdBuiltins(t *testing.T) {
	t.Parallel()

	checker := NewBannedGroupChecker(filepath.Join(t.TempDir(), "db.sqlite"))
	if checker == nil {
		t.Fatalf("expected checker")
	}
	if err := os.MkdirAll(checker.basePath, 0o700); err != nil {
		t.Fatalf("create banned cache dir: %v", err)
	}
	filePath := filepath.Join(checker.basePath, "RHD_banned_groups.json")
	if err := os.Mkdir(filePath, 0o700); err != nil {
		t.Fatalf("create unreadable banned groups path: %v", err)
	}

	banned, err := checker.IsBanned(" rhd ", " MagicX ")
	if err != nil {
		t.Fatalf("check builtin after unreadable cache: %v", err)
	}
	if !banned {
		t.Fatalf("expected MagicX to be banned on RHD")
	}

	banned, err = checker.IsBanned("RHD", "CustomRHD")
	if err == nil {
		t.Fatalf("expected unreadable cache error")
	}
	if !strings.Contains(err.Error(), "RHD_banned_groups.json") {
		t.Fatalf("expected error to include cache path, got %v", err)
	}
	if banned {
		t.Fatalf("expected unread custom group not to be banned without readable cache")
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove unreadable banned groups path: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(`{"banned_groups":"CustomRHD"}`), 0o600); err != nil {
		t.Fatalf("write banned groups: %v", err)
	}
	banned, err = checker.IsBanned("RHD", "CustomRHD")
	if err != nil {
		t.Fatalf("check custom group after readable cache: %v", err)
	}
	if !banned {
		t.Fatalf("expected CustomRHD to be banned after readable cache")
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
