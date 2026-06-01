// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"strings"
	"testing"
)

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
	if !strings.Contains(rendered, "<img src=\"https://pixhost.to/fv71hr.png\" width=\"350\"") {
		t.Fatalf("expected image width preserved, got %q", rendered)
	}
	if strings.Contains(rendered, "&lt;img") || strings.Contains(rendered, "https://pixhost.to/fv71hr.png</a>") {
		t.Fatalf("expected no visible link text duplication, got %q", rendered)
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
