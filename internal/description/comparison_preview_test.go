// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/description"
)

func TestRenderComparisonPreviewFormats(t *testing.T) {
	const labels = "Main input, Source 2, Source 3"
	const urlPrefix = "https://images.example.invalid/"
	urls := make([]string, 15)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s%d.png", urlPrefix, i)
	}
	native := "[comparison=" + labels + "]" + strings.Join(urls, "\n") + "[/comparison]"
	collapse := bbcode.ConvertComparisonToCollapse(native, 1000)
	centered := bbcode.ConvertComparisonToCentered(native, 1000)
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"native", native, "Main input"},
		{"spoiler", collapse, "Maininput"},
		{"hide", strings.NewReplacer("[spoiler=", "[hide=", "[/spoiler]", "[/hide]").Replace(collapse), "Maininput"},
		{"center", centered, "Main input"},
		{"align", strings.NewReplacer("[center]", "[align=center]", "[/center]", "[/align]").Replace(centered), "Main input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preview := urlPrefix + "thumb"
			rendered := description.RenderWithImagePreviews(tc.raw, map[string]string{urls[0]: preview})
			_, target, found := strings.Cut(rendered, `href="`+urls[0]+`">`)
			imageMarkup, _, _ := strings.Cut(target, "</a>")
			if strings.Count(rendered, `class="comparison__row"`) != 5 ||
				strings.Count(rendered, `class="comparison__image"`) != 15 ||
				!strings.Contains(rendered, `class="comparison__figcaption">`+tc.want+`</figcaption>`) ||
				!found || !strings.Contains(imageMarkup, `src="`+preview+`"`) {
				t.Fatalf("expected five rows, three labelled alternatives and paired preview/full URLs: %s", rendered)
			}
			last := -1
			for _, u := range urls {
				next := strings.Index(rendered, `href="`+u+`"`)
				if next <= last {
					t.Fatalf("comparison image %q was lost or reordered", u)
				}
				last = next
			}
		})
	}
}

func TestRenderComparisonPreviewKeepsIncompleteAndUnrelatedContent(t *testing.T) {
	const image = "[url=https://images.example.invalid/sample.png][img=300]https://images.example.invalid/sample.png[/img][/url]"
	for _, raw := range []string{
		"[center]" + image + image + "[/center]",
		"[spoiler=A vs B][center]A | Other[/center]\n" + image + image + "[/spoiler]",
		"[spoiler=A vs B][center]B | A\n" + image + image + "[/center][/spoiler]",
		"[comparison=A,B][url=https://images.example.invalid/album align=center]" + image + image + "[/url][/comparison]",
		"[center]A | B\n" + image + "\n" + image + image + "[/center]",
		"[comparison=A,,B]" + image + image + "[/comparison]",
		"[comparison=A,B][center]" + image + "\n" + image + image + image + "[/center][/comparison]",
	} {
		rendered := description.Render(raw)
		if strings.Contains(rendered, `class="comparison"`) || strings.Count(rendered, "<img ") != strings.Count(raw, "[img=") {
			t.Fatalf("ambiguous markup must keep ordinary images without guessing source assignments: %s", rendered)
		}
	}
}

func TestRenderComparisonPreviewPreservesNativeImageURLs(t *testing.T) {
	const full = "https://images.example.invalid/by-id?x=1,2&v=3"
	raw := "[comparison=Main, Encode, Other cut, Fourth source]" + full + "\n[img]https://images.example.invalid/encode.png?sig=abc[/img]" +
		"\nhttps://images.example.invalid/third.webp, https://images.example.invalid/fourth.jpg[/comparison]"
	rendered := description.Render(raw)
	if strings.Count(rendered, `class="comparison__image"`) != 4 || !strings.Contains(rendered, `href="https://images.example.invalid/by-id?x=1,2&amp;v=3"`) ||
		!strings.Contains(rendered, `src="https://images.example.invalid/encode.png?sig=abc"`) {
		t.Fatalf("native image URLs lost query parameters or extensionless image: %s", rendered)
	}
}

func TestRenderNativeComparisonSupportsPTPCenteredRows(t *testing.T) {
	const base = "https://images.example.invalid/"
	first := "[img]" + base + "first.png[/img]"
	second := "[url=" + base + "full.png?token=one][img]" + base + "thumb.png[/img][/url]"
	row := first + second
	for _, body := range []string{
		"[center]" + first + "\n" + second + "\n" + first + "\n" + second + "[/center]",
		"[center]" + row + "[/center]\n[align=center]" + row + "[/align]",
		"[center]" + base + "first.png\n" + second + "\n" + base + "first.png\n" + second + "[/center]",
	} {
		rendered := description.Render("[comparison=Source, Encode]\n" + body + "\n[/comparison]")
		if strings.Count(rendered, `class="comparison__row"`) != 2 ||
			strings.Count(rendered, `href="`+base+`full.png?token=one"`) != 2 ||
			strings.Count(rendered, `src="`+base+`thumb.png"`) != 2 {
			t.Fatalf("centered native rows lost comparison mappings: %s", rendered)
		}
	}
}

func TestRenderComparisonKeepsExplicitFullImageLink(t *testing.T) {
	const host = "https://images.example.invalid/"
	raw := "[comparison=Main, Encode][url=" + host + "by-id?image=42]\n[img]" + host + "thumb.png[/img]\n[/url]" +
		"[img]" + host + "encode.png[/img][/comparison]"
	rendered := description.Render(raw)
	if !strings.Contains(rendered, `href="`+host+`by-id?image=42"`) ||
		!strings.Contains(rendered, `class="comparison__image comparison__image--linked"`) {
		t.Fatalf("comparison lost its explicit image link: %s", rendered)
	}
}
