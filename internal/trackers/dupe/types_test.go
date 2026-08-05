// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"strings"
	"testing"
)

func TestResolvedPreservesUnknownSearchCompleteness(t *testing.T) {
	t.Parallel()

	search := Resolved(nil, nil).SearchEvidence()
	if search.Complete {
		t.Fatal("compatibility result unexpectedly claimed a complete search")
	}
	if search.Pages != 1 {
		t.Fatalf("compatibility result pages = %d, want 1", search.Pages)
	}
	if len(search.Warnings) != 1 || !strings.Contains(search.Warnings[0], "not evidenced") {
		t.Fatalf("compatibility result warnings = %q, want completeness warning", search.Warnings)
	}
}
