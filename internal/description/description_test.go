// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitTrailingSourceAudioSpoilerPreservesNestedComparison(t *testing.T) {
	comparison := "[spoiler=Comparisons]\n[spoiler=source_audio]copied audio[/spoiler]\n[/spoiler]"
	description := "Notes\n\n" + comparison
	body, audio := SplitTrailingSourceAudioSpoiler(description)
	if body != description || audio != "" {
		t.Fatalf("nested comparison changed: body=%q audio=%q", body, audio)
	}

	generated := "[spoiler=source_audio]generated audio[/spoiler]"
	body, audio = SplitTrailingSourceAudioSpoiler(description + "\n\n" + generated)
	if body != description || audio != generated {
		t.Fatalf("trailing generated audio split incorrectly: body=%q audio=%q", body, audio)
	}
}

func TestRenderBBCode(t *testing.T) {
	rendered := Render("[b]Bold[/b]\n[url=https://example.com]Link[/url]\n[list][*]One[*]Two[/list]")
	if rendered == "" {
		t.Fatalf("expected rendered output")
	}
	if !strings.Contains(rendered, "<b>Bold</b>") {
		t.Fatalf("expected bold tag, got %q", rendered)
	}
	if !strings.Contains(rendered, "<a href=\"https://example.com\">Link</a>") {
		t.Fatalf("expected link tag, got %q", rendered)
	}
	if !strings.Contains(rendered, "<ul>") || !strings.Contains(rendered, "<li>One</li>") {
		t.Fatalf("expected list tags, got %q", rendered)
	}
}

func TestRenderHTMLSanitizes(t *testing.T) {
	rendered := Render("<script>alert(1)</script><b>Safe</b>")
	if strings.Contains(rendered, "script") {
		t.Fatalf("expected script tag to be removed, got %q", rendered)
	}
	if !strings.Contains(rendered, "<b>Safe</b>") {
		t.Fatalf("expected bold tag, got %q", rendered)
	}
}

func TestRenderHTMLWithUnpairedBracketText(t *testing.T) {
	rendered := Render("<p>[b] is a literal marker</p>")
	if rendered != "<p>[b] is a literal marker</p>" {
		t.Fatalf("expected HTML formatting to remain intact, got %q", rendered)
	}
}

func TestRenderSanitizesURLs(t *testing.T) {
	rendered := Render("<a href=\"javascript:alert(1)\">Bad</a>")
	if strings.Contains(rendered, "javascript:") {
		t.Fatalf("expected javascript href to be removed, got %q", rendered)
	}
}

func TestRenderAllowsStyleAlignAndColor(t *testing.T) {
	rendered := Render("[center][color=red]Hi[/color][/center]")
	if !strings.Contains(rendered, "text-align: center") {
		t.Fatalf("expected text-align to be preserved, got %q", rendered)
	}
	if !strings.Contains(rendered, "color: red") {
		t.Fatalf("expected color style to be preserved, got %q", rendered)
	}
}

func TestRenderCodeAllowsNestedColorTags(t *testing.T) {
	input := `[code][color=#bd93f9][b]x64@lost:~$[/b][/color] [color=#f8f8f2]cat ~/release_notes.yml[/color]

[color=#e6c07b]"Release Notes":[/color]
  [color=#61afef]Sources:[/color]
    - [color=#61afef]"Source(1)":[/color] [color=#abb2bf]"Example.Movie.2026.2160p.UHD.Blu-ray.Remux.DV.HDR.HEVC.FLAC2.0-GRP (Video, Audio, Subs) Thanks!"[/color][/code]`
	rendered := Render(input)

	for _, expected := range []string{
		`<pre><code>`,
		`<span style="color: #bd93f9"><b>x64@lost:~$</b></span>`,
		`<span style="color: #f8f8f2">cat ~/release_notes.yml</span>`,
		`<span style="color: #e6c07b">&#34;Release Notes&#34;:</span>`,
		`<span style="color: #61afef">Sources:</span>`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered code color markup %q, got %q", expected, rendered)
		}
	}
	if strings.Contains(rendered, "[color=") || strings.Contains(rendered, "[/color]") {
		t.Fatalf("expected color bbcode tags to be rendered, got %q", rendered)
	}
}

func TestExtractCodeBlocksUsesUniqueNonAliasingPlaceholders(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("outside literal UPBRR_CODE_BLOCK_1\n")
	for i := range 12 {
		content := fmt.Sprintf("block-%d", i)
		if i == 4 {
			content = "inside literal UPBRR_CODE_BLOCK_10"
		}
		builder.WriteString("[code]")
		builder.WriteString(content)
		builder.WriteString("[/code]\n")
	}

	normalized, blocks := extractLiteralBlocks(builder.String())
	if len(blocks) != 12 {
		t.Fatalf("expected 12 code blocks, got %d", len(blocks))
	}
	if !strings.Contains(normalized, "outside literal UPBRR_CODE_BLOCK_1") {
		t.Fatalf("expected outside placeholder-like text preserved, got %q", normalized)
	}

	for i, block := range blocks {
		if block.token == "" {
			t.Fatalf("expected token for block %d", i)
		}
		if strings.Contains(builder.String(), block.token) {
			t.Fatalf("expected generated token for block %d to be absent from original input", i)
		}
		for j, other := range blocks {
			if i == j {
				continue
			}
			if strings.Contains(other.token, block.token) {
				t.Fatalf("expected token for block %d not to alias block %d", i, j)
			}
		}
	}
}

func TestRenderCodeBlocksPreservesLiteralPlaceholderLikeText(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("outside literal UPBRR_CODE_BLOCK_1\n")
	for i := range 12 {
		content := fmt.Sprintf("block-%d", i)
		if i == 4 {
			content = "inside literal UPBRR_CODE_BLOCK_10"
		}
		builder.WriteString("[code]")
		builder.WriteString(content)
		builder.WriteString("[/code]\n")
	}

	rendered := Render(builder.String())

	if strings.Count(rendered, "<pre><code>") != 12 {
		t.Fatalf("expected 12 rendered code blocks, got %q", rendered)
	}
	for _, expected := range []string{
		"outside literal UPBRR_CODE_BLOCK_1",
		"inside literal UPBRR_CODE_BLOCK_10",
		"block-1",
		"block-10",
		"block-11",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderSupportsAlignEqualsBBCode(t *testing.T) {
	rendered := Render("[align=center]Hi[/align]")
	if !strings.Contains(rendered, "text-align: center") {
		t.Fatalf("expected align bbcode to render centered, got %q", rendered)
	}
	if !strings.Contains(rendered, ">Hi<") {
		t.Fatalf("expected text content preserved, got %q", rendered)
	}
}

func TestRenderPreservesSafeHTMLAlignAttribute(t *testing.T) {
	rendered := Render("<div align=\"right\">Hi</div>")
	if !strings.Contains(rendered, "align=\"right\"") {
		t.Fatalf("expected safe align attribute preserved, got %q", rendered)
	}
	if !strings.Contains(rendered, ">Hi<") {
		t.Fatalf("expected text content preserved, got %q", rendered)
	}
}

func TestRenderDoesNotDoubleEscapeHTML(t *testing.T) {
	input := "[quote]Line one\nLine two[/quote]\n[spoiler=BDInfo]Disc Title[/spoiler]"
	rendered := Render(input)
	if strings.Contains(rendered, "&lt;blockquote&gt;") {
		t.Fatalf("expected blockquote tag to be rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "<blockquote>") {
		t.Fatalf("expected blockquote tag, got %q", rendered)
	}
	if !strings.Contains(rendered, "<details>") {
		t.Fatalf("expected details tag, got %q", rendered)
	}
}

func TestRenderComparisonBBCode(t *testing.T) {
	input := "[comparison=Arrow GBR,Capelight Pictures GER]https://pixhost.to/4p352a.png\nhttps://pixhost.to/3bvnbe.png[/comparison]"
	rendered := Render(input)
	if !strings.Contains(rendered, "comparison__screenshots") {
		t.Fatalf("expected comparison markup, got %q", rendered)
	}
	if !strings.Contains(rendered, "comparison__image") {
		t.Fatalf("expected comparison images, got %q", rendered)
	}
	if !strings.Contains(rendered, "pixhost.to/4p352a.png") {
		t.Fatalf("expected comparison image URL, got %q", rendered)
	}
}

func TestRenderLinkedWidthImageBBCode(t *testing.T) {
	input := "[url=https://pixhost.to/fv71hr.png][img width=350]https://pixhost.to/fv71hr.png[/img][/url]"
	rendered := Render(input)
	if !strings.Contains(rendered, "<a href=\"https://pixhost.to/fv71hr.png\">") {
		t.Fatalf("expected linked image wrapper, got %q", rendered)
	}
	if !strings.Contains(rendered, "<img ") ||
		!strings.Contains(rendered, "src=\"https://pixhost.to/fv71hr.png\"") ||
		!strings.Contains(rendered, "width=\"350\"") {
		t.Fatalf("expected image width preserved, got %q", rendered)
	}
	if strings.Contains(rendered, "&lt;img") || strings.Contains(rendered, "https://pixhost.to/fv71hr.png</a>") {
		t.Fatalf("expected no visible link text duplication, got %q", rendered)
	}
}

func TestRenderWithImagePreviewsKeepsFullSizeLinks(t *testing.T) {
	full := "https://images.example.invalid/full.png"
	preview := "https://images.example.invalid/preview.png"
	input := "[url=" + full + "][img]" + full + "[/img][/url]"
	rendered := RenderWithImagePreviews(input, map[string]string{full: preview})
	if !strings.Contains(rendered, `href="`+full+`"`) || !strings.Contains(rendered, `src="`+preview+`"`) ||
		strings.Contains(rendered, `src="`+full+`"`) {
		t.Fatalf("expected preview source and full-size link, got %q", rendered)
	}
	if unsafe := RenderWithImagePreviews(input, map[string]string{full: "javascript:alert(1)"}); !strings.Contains(unsafe, `src="`+full+`"`) {
		t.Fatalf("unsafe preview changed image source: %q", unsafe)
	}
	html := RenderWithImagePreviews(`<div><img src="`+full+`"></div>`, map[string]string{full: preview})
	if !strings.Contains(html, `src="`+preview+`"`) || strings.Contains(html, `src="`+full+`"`) {
		t.Fatalf("HTML image did not use preview: %q", html)
	}
}

func TestRenderCenteredDescriptionImages(t *testing.T) {
	input := `[center][img=300]https://images.example.test/poster.png[/img][/center]

[center]
[url=https://images.example.test/first][img=500]https://images.example.test/first.png[/img][/url] [url=https://images.example.test/second][img=500]https://images.example.test/second.png[/img][/url]
[/center]

[right][url=https://example.test][size=4]Uploaded by example[/size][/url][/right]`
	rendered := Render(input)
	if strings.Count(rendered, `text-align: center`) != 2 || strings.Count(rendered, `<img `) != 3 ||
		strings.Count(rendered, `width="500"`) != 2 || !strings.Contains(rendered, `width="300"`) ||
		!strings.Contains(rendered, `text-align: right`) {
		t.Fatalf("expected centered linked images and right-aligned footer, got %q", rendered)
	}
}

func TestRenderMixedTrackerHTMLAndBBCode(t *testing.T) {
	input := `<center><img src="https://images.example.test/poster.png" style="max-width: 300px;"></center>
[center]Episode overview[/center]
<div style="text-align: right;"><a href="https://example.test">Uploader</a></div>`
	rendered := Render(input)
	if !strings.Contains(rendered, `src="https://images.example.test/poster.png"`) ||
		!strings.Contains(rendered, `style="max-width: min(100%, 300px)"`) ||
		!strings.Contains(rendered, `text-align: center`) ||
		!strings.Contains(rendered, `>Episode overview<`) ||
		!strings.Contains(rendered, `text-align: right`) ||
		strings.Contains(rendered, `[center]`) {
		t.Fatalf("expected HTML and BBCode to render together, got %q", rendered)
	}
}

func TestRenderHTMLImageWidthStyleIsBounded(t *testing.T) {
	rendered := Render(`<img src="https://images.example.test/shot.png" style="max-width: 350px; background-image: url(javascript:alert(1))">`)
	if !strings.Contains(rendered, `style="max-width: min(100%, 350px)"`) ||
		strings.Contains(rendered, "background-image") || strings.Contains(rendered, "javascript:") {
		t.Fatalf("expected only a bounded image width, got %q", rendered)
	}
	for _, unsafe := range []string{"10000px", "calc(100vw)", "300px!important", "-1px"} {
		output := Render(`<img src="https://images.example.test/shot.png" style="max-width: ` + unsafe + `">`)
		if strings.Contains(output, `style=`) {
			t.Fatalf("expected unsafe image width %q to be removed, got %q", unsafe, output)
		}
	}
}

func TestRenderHTMLImageHeightIsBounded(t *testing.T) {
	rendered := Render(`<img src="https://images.example.test/shot.png" height=137>`)
	if !strings.Contains(rendered, `height="137"`) {
		t.Fatalf("expected HDT screenshot height preserved, got %q", rendered)
	}
	for _, unsafe := range []string{"0", "138", "10000", "137px", "-1", "1e3"} {
		output := Render(`<img src="https://images.example.test/shot.png" height="` + unsafe + `">`)
		if strings.Contains(output, `height=`) {
			t.Fatalf("expected unsafe image height %q removed, got %q", unsafe, output)
		}
	}
}

func TestRenderHTMLWithOrdinaryBracketsPreservesEntities(t *testing.T) {
	rendered := Render(`<p>Use [draft] &amp; review</p>`)
	if rendered != `<p>Use [draft] &amp; review</p>` {
		t.Fatalf("expected HTML text and entity to remain intact, got %q", rendered)
	}
}

func TestRenderMixedHTMLEntitiesAndBBCode(t *testing.T) {
	rendered := Render(`<p>First &amp; second</p>[center]A &amp; B[/center]`)
	if strings.Count(rendered, `&amp;`) != 2 || strings.Contains(rendered, `&amp;amp;`) || !strings.Contains(rendered, `text-align: center`) {
		t.Fatalf("expected both HTML and BBCode with single-escaped entities, got %q", rendered)
	}
}

func TestRenderMixedHTMLWithQuotedAngleBracket(t *testing.T) {
	rendered := Render(`<a title="2 > 1" href="https://example.invalid">link</a>[b]tail[/b]`)
	if !strings.Contains(rendered, `title="2 &gt; 1"`) ||
		!strings.Contains(rendered, `href="https://example.invalid"`) ||
		!strings.Contains(rendered, `>link</a>`) ||
		!strings.Contains(rendered, `<b>tail</b>`) {
		t.Fatalf("expected intact HTML attribute and BBCode, got %q", rendered)
	}
}

func TestRenderMixedHTMLKeepsBBCodeLookingAttributes(t *testing.T) {
	rendered := Render(`<a title="[code]literal[/code]" href="https://example.invalid/[pre]path[/pre]">x</a>[b]tail[/b]`)
	if !strings.Contains(rendered, `title="[code]literal[/code]"`) ||
		!strings.Contains(rendered, `href="https://example.invalid/[pre]path[/pre]"`) ||
		!strings.Contains(rendered, `<b>tail</b>`) {
		t.Fatalf("expected HTML attributes to remain literal while BBCode text renders, got %q", rendered)
	}
}

func TestRenderHTMLKeepsUnmatchedBBCodeClosingText(t *testing.T) {
	rendered := Render(`<a title="[code]" href="https://example.invalid">x</a>[/code]`)
	if !strings.Contains(rendered, `title="[code]"`) ||
		!strings.Contains(rendered, `>x</a>[/code]`) {
		t.Fatalf("expected unmatched BBCode-like text to remain visible, got %q", rendered)
	}
}

func TestRenderMixedHTMLCommentDoesNotCaptureBBCodeLiteralBlock(t *testing.T) {
	rendered := Render(`<!-- [code] --><a href="https://example.invalid">x</a> ordinary [/code][b]tail[/b]`)
	if !strings.Contains(rendered, `<a href="https://example.invalid">x</a> ordinary `) ||
		!strings.Contains(rendered, `<b>tail</b>`) {
		t.Fatalf("expected comment BBCode to leave visible HTML and text intact, got %q", rendered)
	}
}

func TestRenderHTMLCommentDoesNotTriggerBBCodeMode(t *testing.T) {
	rendered := Render(`<!-- [code] --><a href="https://example.invalid">x</a> ordinary [/code]`)
	if !strings.Contains(rendered, `<a href="https://example.invalid">x</a> ordinary [/code]`) {
		t.Fatalf("expected comment BBCode to remain outside mixed-mode detection, got %q", rendered)
	}
}

func TestRenderDiscardedHTMLDoesNotTriggerBBCodeMode(t *testing.T) {
	for _, tag := range []string{"script", "style"} {
		t.Run(tag, func(t *testing.T) {
			rendered := Render("<" + tag + ">[b]</" + tag + "><a href=\"https://example.invalid\">x</a>[/b]")
			if !strings.Contains(rendered, `<a href="https://example.invalid">x</a>[/b]`) ||
				strings.Contains(rendered, "[b]") {
				t.Fatalf("expected discarded HTML to leave visible text unchanged, got %q", rendered)
			}
		})
	}
}

func TestRenderHTMLLiteralDoesNotTriggerBBCodeMode(t *testing.T) {
	for _, tag := range []string{"pre", "code"} {
		t.Run(tag, func(t *testing.T) {
			rendered := Render("<" + tag + ">[b]literal</" + tag + ">[/b]")
			if !strings.Contains(rendered, "<"+tag+">[b]literal</"+tag+">[/b]") {
				t.Fatalf("expected HTML literal to leave unmatched closing text intact, got %q", rendered)
			}
		})
	}
}

func TestRenderKeepsMalformedHTMLTailVisible(t *testing.T) {
	for _, input := range []string{`hello <img`, `hello [b]world[/b] <img`} {
		t.Run(input, func(t *testing.T) {
			rendered := Render(input)
			if !strings.Contains(rendered, `&lt;img`) || !strings.Contains(rendered, `hello `) {
				t.Fatalf("expected malformed HTML tail to remain visible, got %q", rendered)
			}
		})
	}
}

func TestRenderMixedHTMLPreKeepsBBCodeLiteral(t *testing.T) {
	rendered := Render(`<pre>[b]literal[/b] &amp; raw</pre>[b]outside[/b]`)
	if !strings.Contains(rendered, `<pre>[b]literal[/b] &amp; raw</pre>`) ||
		!strings.Contains(rendered, `<b>outside</b>`) {
		t.Fatalf("expected HTML pre content to remain literal, got %q", rendered)
	}
}

func TestRenderMixedHTMLPreKeepsNestedBBCodeLiteral(t *testing.T) {
	rendered := Render(`<pre>[code]literal[/code]</pre>[b]outside[/b]`)
	if !strings.Contains(rendered, `<pre>[code]literal[/code]</pre>`) ||
		!strings.Contains(rendered, `<b>outside</b>`) {
		t.Fatalf("expected HTML pre content to remain literal, got %q", rendered)
	}
}

func TestRenderMixedHTMLCodeKeepsBBCodeLiteral(t *testing.T) {
	rendered := Render(`<code>[i]literal[/i]</code>[i]outside[/i]`)
	if !strings.Contains(rendered, `<code>[i]literal[/i]</code>`) ||
		!strings.Contains(rendered, `<i>outside</i>`) {
		t.Fatalf("expected HTML code content to remain literal, got %q", rendered)
	}
}

func TestRenderMixedUnclosedHTMLLiteralKeepsBBCodeLiteral(t *testing.T) {
	for _, tag := range []string{"pre", "code"} {
		t.Run(tag, func(t *testing.T) {
			rendered := Render(`<div>[b]outside[/b]</div><` + tag + `>[b]literal[/b]`)
			if !strings.Contains(rendered, `<div><b>outside</b></div>`) ||
				!strings.Contains(rendered, `<`+tag+`>[b]literal[/b]</`+tag+`>`) {
				t.Fatalf("expected malformed HTML literal block to protect BBCode, got %q", rendered)
			}
		})
	}
}

func TestRenderPreKeepsEmbeddedBBCodeLiteral(t *testing.T) {
	rendered := Render(`[pre][code][b]literal[/b][/code] & <test>[/pre]`)
	if !strings.Contains(rendered, `<pre>[code][b]literal[/b][/code] &amp; &lt;test&gt;</pre>`) {
		t.Fatalf("expected literal escaped pre content, got %q", rendered)
	}
}

func TestRenderBBCodeLiteralBlocksEscapeNestedHTML(t *testing.T) {
	for _, block := range []string{"code", "pre"} {
		for _, tag := range []string{"code", "pre"} {
			t.Run(block+"/"+tag, func(t *testing.T) {
				rendered := Render("[" + block + "]<" + tag + ">literal</" + tag + ">[/" + block + "]")
				if !strings.Contains(rendered, "&lt;"+tag+"&gt;literal&lt;/"+tag+"&gt;") {
					t.Fatalf("expected nested HTML tag to remain literal, got %q", rendered)
				}
			})
		}
	}
}

func TestRenderLiteralBlocksPreservePlaceholderLikeText(t *testing.T) {
	input := `[pre]UPBRR_HTML_TAG_PLACEHOLDER_0_END[/pre]<img src="https://images.example.invalid/pixel.png">` +
		`[code]UPBRR_HTML_ENTITY_PLACEHOLDER_0_END[/code] &amp; [b]safe[/b]`
	rendered := Render(input)
	if !strings.Contains(rendered, `<pre>UPBRR_HTML_TAG_PLACEHOLDER_0_END</pre>`) ||
		!strings.Contains(rendered, `<pre><code>UPBRR_HTML_ENTITY_PLACEHOLDER_0_END</code></pre>`) ||
		strings.Count(rendered, `<img `) != 1 ||
		!strings.Contains(rendered, `&amp;`) ||
		!strings.Contains(rendered, `<b>safe</b>`) {
		t.Fatalf("expected literal placeholders and one image, got %q", rendered)
	}
}

func TestRenderTrackerMediaInfoWrappers(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "pre", input: "[pre]" + sampleMediaInfoText() + "[/pre]"},
		{name: "hide pre", input: "[hide=DVD MediaInfo][pre]" + sampleMediaInfoText() + "[/pre][/hide]"},
		{name: "localized spoiler", input: "[spoiler=Informações do Arquivo][left][font=Courier New]" + sampleMediaInfoText() + "[/font][/left][/spoiler]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered := Render(test.input)
			if !strings.Contains(rendered, `class="mediainfo"`) || !strings.Contains(rendered, `<summary>Raw `) {
				t.Fatalf("expected structured MediaInfo preview, got %q", rendered)
			}
		})
	}
}

func TestRenderBBCodeWithHTMLLikeComparisonText(t *testing.T) {
	input := "[align=center][url=https://images.example.invalid/view][img width=350]https://images.example.invalid/thumb.jpg[/img][/url][/align]\n" +
		"[spoiler=Comparison]<strong>Source</strong>[/spoiler]"
	rendered := Render(input)
	if !strings.Contains(rendered, `src="https://images.example.invalid/thumb.jpg"`) ||
		!strings.Contains(rendered, `width="350"`) ||
		!strings.Contains(rendered, `href="https://images.example.invalid/view"`) {
		t.Fatalf("expected BHD BBCode screenshot preview, got %q", rendered)
	}
	if strings.Contains(rendered, "[img") || strings.Contains(rendered, "[align") {
		t.Fatalf("expected BBCode tags to render, got %q", rendered)
	}
}

func TestRenderMediaInfoSpoilerPreview(t *testing.T) {
	input := "[spoiler=MediaInfo][code]" + sampleMediaInfoText() + "[/code][/spoiler]"
	rendered := Render(input)
	for _, expected := range []string{
		`class="mediainfo"`,
		`class="mediainfo__general"`,
		`class="mediainfo__video"`,
		`class="mediainfo__audio"`,
		`Movie.2024.1080p.mkv`,
		`14.6 Mb/s`,
		`AVC (8 bits)`,
		`1 920 pixels x 1 080 pixels`,
		`English / DTS / 6 channels / 1 509 kb/s / Main Audio`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected %q in rendered mediainfo, got %q", expected, rendered)
		}
	}
}

func TestRenderMediaInfoTagPreview(t *testing.T) {
	rendered := Render("[mediainfo]" + sampleMediaInfoText() + "[/mediainfo]")
	if !strings.Contains(rendered, `class="mediainfo"`) {
		t.Fatalf("expected mediainfo preview, got %q", rendered)
	}
	if !strings.Contains(rendered, `<summary>Raw MediaInfo</summary>`) {
		t.Fatalf("expected raw mediainfo details, got %q", rendered)
	}
}

func TestRenderVOBMediaInfoKeepsRawDump(t *testing.T) {
	rendered := Render("[spoiler=VOB MediaInfo][code]" + sampleMediaInfoText() + "[/code][/spoiler]")
	if !strings.Contains(rendered, `<summary>Raw VOB MediaInfo</summary>`) {
		t.Fatalf("expected VOB MediaInfo raw summary, got %q", rendered)
	}
	if !strings.Contains(rendered, `Writing library : x264`) {
		t.Fatalf("expected raw dump to be preserved, got %q", rendered)
	}
	if strings.Contains(rendered, `mediainfo__encode-settings`) || strings.Contains(rendered, `Encode settings`) {
		t.Fatalf("did not expect encode settings section, got %q", rendered)
	}
}

func TestRenderHDBTransformedMediaInfoPreview(t *testing.T) {
	rendered := Render("[hide=MediaInfo][font=monospace]" + sampleMediaInfoText() + "[/font][/hide]")
	if !strings.Contains(rendered, `class="mediainfo"`) {
		t.Fatalf("expected mediainfo preview for HDB transformed block, got %q", rendered)
	}
	if !strings.Contains(rendered, `<summary>Raw MediaInfo</summary>`) {
		t.Fatalf("expected raw mediainfo details, got %q", rendered)
	}
}

func TestRenderQuoteVOBMediaInfoPreview(t *testing.T) {
	rendered := Render("[quote=VOB MediaInfo]" + sampleMediaInfoText() + "[/quote]")
	if !strings.Contains(rendered, `class="mediainfo"`) {
		t.Fatalf("expected mediainfo preview for quote block, got %q", rendered)
	}
	if !strings.Contains(rendered, `<summary>Raw VOB MediaInfo</summary>`) {
		t.Fatalf("expected VOB MediaInfo raw summary, got %q", rendered)
	}
}

func TestRenderHideCodeMediaInfoPreview(t *testing.T) {
	rendered := Render("[hide][code]" + sampleMediaInfoText() + "[/code][/hide]")
	if !strings.Contains(rendered, `class="mediainfo"`) {
		t.Fatalf("expected mediainfo preview for hide code block, got %q", rendered)
	}
}

func TestRenderMalformedMediaInfoFallsBack(t *testing.T) {
	input := "[spoiler=MediaInfo][code]not really mediainfo[/code][/spoiler]"
	rendered := Render(input)
	if strings.Contains(rendered, `class="mediainfo"`) {
		t.Fatalf("did not expect mediainfo preview, got %q", rendered)
	}
	if !strings.Contains(rendered, "<details>") || !strings.Contains(rendered, "not really mediainfo") {
		t.Fatalf("expected existing spoiler rendering fallback, got %q", rendered)
	}
}

func TestRenderMediaInfoSanitizesPreviewHTML(t *testing.T) {
	input := "[mediainfo]" + sampleMediaInfoText() + "\nTitle : <script>alert(1)</script> Safe[/mediainfo]"
	rendered := Render(input)
	if strings.Contains(rendered, "<script") {
		t.Fatalf("expected script tag to be removed, got %q", rendered)
	}
	if !strings.Contains(rendered, `class="mediainfo__raw"`) {
		t.Fatalf("expected safe mediainfo class to be preserved, got %q", rendered)
	}
}

func TestRenderMediaInfoSourcePreview(t *testing.T) {
	t.Parallel()
	rendered := RenderMediaInfo(sampleMediaInfoText() + "\nTitle : <script>alert(1)</script> Safe")
	if !strings.Contains(rendered, `class="mediainfo-preview"`) || !strings.Contains(rendered, `class="mediainfo__raw"`) {
		t.Fatalf("expected structured MediaInfo and raw report, got %q", rendered)
	}
	if strings.Contains(rendered, "<script") {
		t.Fatalf("expected escaped source text, got %q", rendered)
	}
	if got := RenderMediaInfo("  "); got != "" {
		t.Fatalf("blank report rendered as %q", got)
	}
	fallback := RenderMediaInfo("Unrecognized <script>text</script>")
	if !strings.Contains(fallback, "<pre><code>") || strings.Contains(fallback, "<script") {
		t.Fatalf("expected sanitized raw fallback, got %q", fallback)
	}
}

func sampleMediaInfoText() string {
	return `General
Complete name : C:\Media\Movie.2024.1080p.mkv
Format : Matroska
File size : 10.4 GiB
Duration : 1 h 42 min
Overall bit rate : 14.6 Mb/s

Video
Format : AVC
Bit depth : 8 bits
Width : 1 920 pixels
Height : 1 080 pixels
Display aspect ratio : 16:9
Frame rate : 23.976 FPS
Bit rate : 12.0 Mb/s
Writing library : x264
Encoding settings : cabac=1 / ref=5

Audio
Format : DTS
Language : English
Channel(s) : 6 channels
Bit rate : 1 509 kb/s
Title : Main Audio

Text
Format : UTF-8
Language : English
Title : SDH`
}
