// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"
	"testing"
)

func TestFinalizeDescription(t *testing.T) {
	input := "[center][img=350]https://img.example/a.png[/img][/center]\n[sup]x[/sup]\n[sub]y[/sub]\n[list]z[/list]"
	got := finalizeDescription(input)
	if !strings.Contains(got, "[align=center][img]https://img.example/a.png[/img][/align]") {
		t.Fatalf("expected center/img tags normalized for ANT, got %q", got)
	}
	if strings.Contains(got, "[sup]") || strings.Contains(got, "[sub]") || strings.Contains(got, "[list]") {
		t.Fatalf("expected ANT cleanup to strip unsupported tags, got %q", got)
	}
}
