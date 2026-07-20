// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import (
	"strings"
	"testing"
)

func TestFinalizeTrackerDescriptionANT(t *testing.T) {
	input := "[center][img=350]https://img.example/a.png[/img][/center]\n[sup]x[/sup]\n[sub]y[/sub]\n[list]z[/list]"
	got := FinalizeTrackerDescription("ANT", input)
	if !strings.Contains(got, "[align=center][img]https://img.example/a.png[/img][/align]") {
		t.Fatalf("expected center/img tags normalized for ANT, got %q", got)
	}
	if strings.Contains(got, "[sup]") || strings.Contains(got, "[sub]") || strings.Contains(got, "[list]") {
		t.Fatalf("expected ANT cleanup to strip unsupported tags, got %q", got)
	}
}

func TestFinalizeTrackerDescriptionPTS(t *testing.T) {
	input := "[hide]x[/hide]\n[comparison=A, B]https://img.example/a.png https://img.example/b.png[/comparison]"
	got := FinalizeTrackerDescription("PTS", input)
	if strings.Contains(got, "[hide]") {
		t.Fatalf("expected hide tags removed for PTS, got %q", got)
	}
	if !strings.Contains(got, "[center]A | B") {
		t.Fatalf("expected comparison centered for PTS, got %q", got)
	}
}

func TestFinalizeTrackerDescriptionTL(t *testing.T) {
	input := "[center]x[/center]\n[comparison=A, B]https://img.example/a.png https://img.example/b.png[/comparison]\n[note]n[/note]"
	got := FinalizeTrackerDescription("TL", input)
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

func TestFinalizeTrackerDescriptionFLD(t *testing.T) {
	input := "[user]John[/user]\n[img]https://img.example/a.png[/img]\n[img width=350]https://img.example/b.png[/img]"
	got := FinalizeTrackerDescription("FLD", input)
	if strings.Contains(got, "[user]") || strings.Contains(got, "[/user]") {
		t.Fatalf("expected user tags removed for FLD, got %q", got)
	}
	if !strings.Contains(got, "[img width=300]https://img.example/a.png[/img]") {
		t.Fatalf("expected img tags resized to 300 for FLD, got %q", got)
	}
	if !strings.Contains(got, "[img width=350]https://img.example/b.png[/img]") {
		t.Fatalf("expected pre-sized img tags preserved for FLD, got %q", got)
	}
}
