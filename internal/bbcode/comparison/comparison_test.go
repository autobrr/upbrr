// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparison

import (
	"reflect"
	"testing"
)

func TestParsePreservesComparisonMatrixAndOriginalMarkup(t *testing.T) {
	t.Parallel()
	raw := "[comparison=Source A, Source B]\r\n[url=https://images.example.test/a1.png][img]https://images.example.test/a1.png[/img][/url]\n" +
		"https://images.example.test/b1.png https://images.example.test/a2.png https://images.example.test/b2.png[/comparison]"
	blocks := Parse("Before\n" + raw + "\nAfter")
	if len(blocks) != 1 || blocks[0].BBCode != raw || !reflect.DeepEqual(blocks[0].Sources, []string{"Source A", "Source B"}) ||
		!reflect.DeepEqual(blocks[0].Images, []string{"https://images.example.test/a1.png", "https://images.example.test/b1.png", "https://images.example.test/a2.png", "https://images.example.test/b2.png"}) {
		t.Fatalf("matrix=%+v", blocks)
	}
	if blocks := Parse("[comparison=A,B]https://images.example.test/a.png"); len(blocks) != 0 {
		t.Fatalf("unclosed comparison became upload authority: %+v", blocks)
	}
}

func TestParseComparisonImageReferences(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want []string
	}{
		{
			name: "queries and fragments",
			body: "https://images.example.test/a.png?key=synthetic&size=1#frame https://images.example.test/b.png?key=other",
			want: []string{"https://images.example.test/a.png?key=synthetic&size=1#frame", "https://images.example.test/b.png?key=other"},
		},
		{
			name: "adjacent images",
			body: "[img]https://images.example.test/a.png[/img][img]https://images.example.test/b.png[/img]",
			want: []string{"https://images.example.test/a.png", "https://images.example.test/b.png"},
		},
		{
			name: "linked originals",
			body: "[url=https://images.example.test/a.png?key=synthetic][img]https://images.example.test/a-thumb.png[/img][/url]" +
				"[url=https://images.example.test/b.png][img]https://images.example.test/b-thumb.png[/img][/url]",
			want: []string{"https://images.example.test/a.png?key=synthetic", "https://images.example.test/b.png"},
		},
		{
			name: "image attributes and extensionless endpoints",
			body: `[img url="https://images.example.test/image?id=1&size=2" width=300][/img][img width=300]https://images.example.test/image?id=2[/img]`,
			want: []string{"https://images.example.test/image?id=1&size=2", "https://images.example.test/image?id=2"},
		},
		{
			name: "entities and line breaks",
			body: "https://images.example.test/a.png?key=synthetic&amp;size=1\r\nhttps://images.example.test/b.png",
			want: []string{"https://images.example.test/a.png?key=synthetic&size=1", "https://images.example.test/b.png"},
		},
		{
			name: "HTML and Unicode whitespace boundaries",
			body: "https://images.example.test/a.png&nbsp;https://images.example.test/b.png\u2003https://images.example.test/c.png",
			want: []string{"https://images.example.test/a.png", "https://images.example.test/b.png", "https://images.example.test/c.png"},
		},
		{
			name: "entity decoding occurs once for every reference form",
			body: "https://images.example.test/a.png?x=1&amp;amp;y=2 " +
				"[img]https://images.example.test/b.png?x=1&amp;amp;y=2[/img]" +
				"[url=https://images.example.test/c.png?x=1&amp;amp;y=2][img]https://images.example.test/thumb.png[/img][/url]",
			want: []string{"https://images.example.test/a.png?x=1&amp;y=2", "https://images.example.test/b.png?x=1&amp;y=2", "https://images.example.test/c.png?x=1&amp;y=2"},
		},
		{
			name: "encoded punctuation stays URL data",
			body: "https://images.example.test/a.png?filter=&#91;v&#93;&amp;size=1&nbsp;https://images.example.test/b.png",
			want: []string{"https://images.example.test/a.png?filter=[v]&size=1", "https://images.example.test/b.png"},
		},
		{
			name: "page wrappers retain embedded images",
			body: "[url=https://images.example.test/view/1][img]https://images.example.test/a.png[/img][/url] " +
				"[url=https://images.example.test/view/2][img]https://images.example.test/b.png[/img][/url]",
			want: []string{"https://images.example.test/a.png", "https://images.example.test/b.png"},
		},
		{name: "non image text and unsafe schemes", body: "https://images.example.test/notes.txt [img]javascript:alert(1)[/img]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := "[comparison='Source A, Source B']" + test.body + "[/comparison]"
			blocks := Parse(raw)
			if len(blocks) != 1 || blocks[0].BBCode != raw || !reflect.DeepEqual(blocks[0].Images, test.want) ||
				!reflect.DeepEqual(blocks[0].Sources, []string{"Source A", "Source B"}) {
				t.Fatalf("image references=%+v want=%v", blocks, test.want)
			}
		})
	}
}

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

func TestBlockRangesProtectsCompsCollectionLinks(t *testing.T) {
	t.Parallel()
	for _, title := range []string{"Comps", "comps", "COMPS"} {
		block := "[spoiler=" + title + "][url=https://slow.pics/c/synthetic]HDR and audio data[/url][/spoiler]"
		ranges := BlockRanges(block)
		if len(ranges) != 1 || ranges[0] != [2]int{0, len(block)} {
			t.Fatalf("collection block not protected: %q %v", block, ranges)
		}
		if got := MapOutsideBlocks(block, func(string) string { return "" }); got != block {
			t.Fatalf("collection link rewritten: %q", got)
		}
	}
}
