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

func TestFinalizeDescriptionPreservesSupportedContent(t *testing.T) {
	t.Parallel()
	input := "[b]Bold[/b] [i]Italic[/i] [u]Underline[/u] [s]Strike[/s]\n" +
		"[align=right][color=red]Red[/color][/align]\n" +
		"[quote=Encoder]Notes[/quote]\n[code]Block text[/code] [pre]Inline text[/pre]\n" +
		"[*]First\n[*]Second\n[hide=Details]Details[/hide]\n" +
		"[url=https://example.invalid/reference]Reference[/url]\n" +
		"[img]https://images.example.invalid/artwork.png[/img]"
	if got := finalizeDescription(input); got != input {
		t.Fatalf("supported content changed: %q", got)
	}
}

func TestFinalizeDescriptionUsesConservativeSubstitutes(t *testing.T) {
	t.Parallel()
	input := "[h1]Heading[/h1]\n[size=20]Large text[/size]\n" +
		"[ul][li]First[/li][li]Second[/li][/ul]\n" +
		"[spoiler=Details][center][img=350]https://images.example.invalid/artwork.png[/img][/center][/spoiler]"
	want := "[b]Heading[/b]\nLarge text\n- First\n- Second\n\n" +
		"[hide=Details][align=center][img]https://images.example.invalid/artwork.png[/img][/align][/hide]"
	if got := finalizeDescription(input); got != want {
		t.Fatalf("substituted content = %q, want %q", got, want)
	}
}

func TestFinalizeDescriptionPreservesLiteralBlocks(t *testing.T) {
	t.Parallel()
	const markup = "[h1]Example[/h1]\n[center][img=350]https://images.example.invalid/example.png[/img][/center]\n" +
		"[spoiler=Details][size=20]Text[/size][/spoiler]\n&lt;literal&gt;"
	for _, tag := range []string{"code", "pre"} {
		t.Run(tag, func(t *testing.T) {
			literal := "[" + tag + "]" + markup + "[/" + tag + "]"
			got := finalizeDescription(literal + "\n[h1]Outside[/h1]")
			if want := literal + "\n[b]Outside[/b]"; got != want {
				t.Fatalf("literal markup changed: %q, want %q", got, want)
			}
		})
	}
}
