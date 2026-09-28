// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparison

import "testing"

func TestBlockRangesIncludesNestedSpoiler(t *testing.T) {
	first := "[spoiler=Comparisons]\r\n[spoiler=Source][img]https://img.example/a.png[/img][/spoiler]\r\n" +
		"[img]https://img.example/b.png[/img]\r\n[/spoiler]"
	second := "[comparison=Source, Encode][img]https://img.example/c.png[/img][/comparison]"
	source := "before\n" + first + "\n" + second + "\nafter"
	ranges := BlockRanges(source)
	if len(ranges) != 2 || source[ranges[0][0]:ranges[0][1]] != first || source[ranges[1][0]:ranges[1][1]] != second {
		t.Fatalf("comparison ranges = %#v", ranges)
	}
	if got := RemoveComparisonBlocks(source); got != "before\n\n\nafter" {
		t.Fatalf("remaining description = %q", got)
	}
}

func TestMapOutsideBlocksPreservesNestedComparison(t *testing.T) {
	block := "[spoiler=Comparisons][spoiler=Source][img]https://img.example/a.png[/img][/spoiler][/spoiler]"
	transform := func(value string) string { return "<" + value + ">" }
	if got, want := MapOutsideBlocks("before"+block+"after", transform), "<before>"+block+"<after>"; got != want {
		t.Fatalf("mapped description = %q, want %q", got, want)
	}
	if got, want := MapOutsideBlocks(block, transform), "<>"+block+"<>"; got != want {
		t.Fatalf("mapped block = %q, want %q", got, want)
	}
	if got := MapOutsideBlocks("plain", transform); got != "<plain>" {
		t.Fatalf("mapped plain text = %q", got)
	}
}
