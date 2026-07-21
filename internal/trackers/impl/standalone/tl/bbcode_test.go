// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"strings"
	"testing"
)

func TestFinalizeDescription(t *testing.T) {
	input := "[center]x[/center]\n[comparison=A, B]https://img.example/a.png https://img.example/b.png[/comparison]\n[note]n[/note]"
	got := finalizeDescription(input)
	if !strings.Contains(got, "<center>x</center>") {
		t.Fatalf("expected center tags converted for TL, got %q", got)
	}
	if !strings.Contains(got, "Note: n") {
		t.Fatalf("expected note tag rewritten for TL, got %q", got)
	}
	if strings.Contains(got, "[comparison=") {
		t.Fatalf("expected comparison converted for TL, got %q", got)
	}
}
