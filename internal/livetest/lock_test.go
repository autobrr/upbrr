// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package livetest

import "testing"

func TestLockExcludesConcurrentRuntimeAndReleasesOnClose(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("HOME", base)
	first, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Lock(); err == nil {
		_ = second.Close()
		t.Fatal("a second runtime acquired the live-test lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Lock()
	if err != nil {
		t.Fatalf("closed owner retained lock: %v", err)
	}
	defer third.Close()
}
