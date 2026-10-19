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
	got := buildDescription("Release notes\n\n[spoiler=Audio Plots]\n[img]https://images.example.invalid/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]")
	for _, token := range []string{"Release notes", "<details>", "<img", "https://images.example.invalid/audio.png", "<pre><code>Peak: -1 dB</code></pre>"} {
		if !strings.Contains(got, token) {
			t.Fatalf("missing %q in description %q", token, got)
		}
	}
	if strings.Index(got, "Release notes") > strings.Index(got, "<details>") {
		t.Fatalf("audio precedes notes: %q", got)
	}
}

func TestBuildDescriptionAudioPayload(t *testing.T) {
	t.Parallel()
	got := buildDescription("Release notes\n\n[spoiler=Audio Plots]\n[img]https://images.example.invalid/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]")
	want := "Release notes<br>\n<details><summary>Audio Plots</summary><br /><img src=\"https://images.example.invalid/audio.png\" /><br /><pre><code>Peak: -1 dB</code></pre><br /></details>"
	if got != want {
		t.Fatalf("audio payload changed:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildDescriptionKeepsDirectAnalysisLinkOutsideAudioSpoiler(t *testing.T) {
	t.Parallel()
	got := buildDescription("Notes\n\n[spoiler=Comps]\n[url=https://slow.pics/c/comparison]Comparison[/url]\n[/spoiler]\n\n" +
		"[spoiler=Audio Plots]\n[code]Synthetic amplitude statistics[/code]\n[/spoiler]\n\n" +
		"[url=https://slow.pics/c/synthetic]HDR and audio data[/url]")
	if !strings.Contains(got, `href="https://slow.pics/c/synthetic"`) || !strings.Contains(got, "HDR and audio data</a>") ||
		!strings.Contains(got, `href="https://slow.pics/c/comparison"`) ||
		!strings.Contains(got, "<pre><code>Synthetic amplitude statistics</code></pre>") ||
		strings.Index(got, "</details>") > strings.Index(got, `href="https://slow.pics/c/synthetic"`) {
		t.Fatalf("direct analysis link lost or nested: %q", got)
	}
}

func TestBuildDescriptionKeepsComparisonViewerLinkAndLiteralExamples(t *testing.T) {
	t.Parallel()
	for _, caption := range []string{"Source A vs Source B", "<b>Source A vs Source B</b>"} {
		link := "[url=https://slow.pics/c/SyntheticComparison]" + caption + "[/url]"
		got := buildDescription("Before\n" + link + "\nAfter\n[code]" + link + "[/code]\n<pre>" + link + "</pre>\n<code>" + link +
			"</code>\n<!--" + link + "-->\n<span data-example='" + link + "'>notes</span>")
		if strings.Count(got, `href="https://slow.pics/c/SyntheticComparison"`) != 1 || !strings.Contains(got, "Before") || !strings.Contains(got, "After") ||
			strings.Contains(got, "UPBRR_") || !strings.Contains(got, caption) {
			t.Fatalf("comparison link lost or literal example activated: %q", got)
		}
	}
}
