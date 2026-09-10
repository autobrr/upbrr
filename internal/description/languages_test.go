// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestManualLanguageBlock(t *testing.T) {
	t.Parallel()

	got := ManualLanguageBlock(api.ManualLanguageFacts{
		Audio:              []string{"English", "Brazilian Portuguese", "english"},
		Subtitles:          []string{"French", "[b]Injected[/b]"},
		HardcodedSubtitles: []string{"English - Forced"},
	})
	want := "Audio Language/s: English, Brazilian Portuguese\n" +
		"Subtitle Language/s: French, &#91;b&#93;Injected&#91;/b&#93;\n" +
		"Hardcoded Subtitle Language/s: English - Forced"
	if got != want {
		t.Fatalf("block=%q, want %q", got, want)
	}
}

func TestManualLanguageBlockOmitsEmptyLists(t *testing.T) {
	t.Parallel()

	if got := ManualLanguageBlock(api.ManualLanguageFacts{Audio: []string{"", " "}}); got != "" {
		t.Fatalf("block=%q, want empty", got)
	}
}
