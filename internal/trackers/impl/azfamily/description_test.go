// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import "testing"

func TestBuildDescriptionRemovesImportedUpbrrSignature(t *testing.T) {
	got := buildDescription("Release notes\n[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]")
	if got != "Release notes" {
		t.Fatalf("unexpected description: %q", got)
	}
}
