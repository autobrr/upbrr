// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"
	"testing"
)

func TestBuildDescriptionRemovesImportedUpbrrSignature(t *testing.T) {
	got := buildDescription("Release notes\n[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]")
	if got != "Release notes" {
		t.Fatalf("unexpected description: %q", got)
	}
}

func TestBuildDescriptionRendersAudioAnalysisAfterNotes(t *testing.T) {
	t.Parallel()
	got := buildDescription("Release notes\n\n[spoiler=source_audio]\n[img]https://images.example.invalid/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]")
	for _, token := range []string{"Release notes", "<details>", "<img", "https://images.example.invalid/audio.png", "<pre><code>Peak: -1 dB</code></pre>"} {
		if !strings.Contains(got, token) {
			t.Fatalf("missing %q in description %q", token, got)
		}
	}
	if strings.Index(got, "Release notes") > strings.Index(got, "<details>") {
		t.Fatalf("audio precedes notes: %q", got)
	}
}
