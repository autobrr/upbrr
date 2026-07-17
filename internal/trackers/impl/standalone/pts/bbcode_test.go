// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"strings"
	"testing"
)

func TestFinalizeDescription(t *testing.T) {
	input := "[hide]x[/hide]\n[comparison=A, B]https://img.example/a.png https://img.example/b.png[/comparison]"
	got := finalizeDescription(input)
	if strings.Contains(got, "[hide]") {
		t.Fatalf("expected hide tags removed for PTS, got %q", got)
	}
	if !strings.Contains(got, "[center]A | B") {
		t.Fatalf("expected comparison centered for PTS, got %q", got)
	}
}
