// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestHistoryStatusLabel(t *testing.T) {
	t.Parallel()

	if got := HistoryStatusLabel("custom--status", 0); got != "Custom Status" {
		t.Fatalf("custom status label = %q, want %q", got, "Custom Status")
	}
	if got := HistoryStatusLabel("", 1); got != "Rule Issues" {
		t.Fatalf("rule failure status label = %q, want %q", got, "Rule Issues")
	}
}
