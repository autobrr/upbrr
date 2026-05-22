// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	stdhtml "html"
	"regexp"
	"strconv"
	"strings"
)

type mediaInfoBlock struct {
	start int
	end   int
	label string
	raw   string
}

type mediaInfoSection struct {
	name   string
	fields map[string]string
}

type mediaInfoSummary struct {
	label    string
	raw      string
	filename string
	general  map[string]string
	video    []map[string]string
	audio    []map[string]string
	text     []map[string]string
}

type mediaInfoField struct {
	label string
	value string
}

var (
	mediaInfoBBCodePattern     = regexp.MustCompile(`(?is)\[mediainfo\]([\s\S]*?)\[/mediainfo\]`)
	mediaInfoSpoilerPattern    = regexp.MustCompile(`(?is)\[spoiler=([^\]]*mediainfo[^\]]*)\]\s*\[code\]([\s\S]*?)\[/code\]\s*\[/spoiler\]`)
	mediaInfoHideCodePattern   = regexp.MustCompile(`(?is)\[hide(?:=([^\]]*mediainfo[^\]]*))?\]\s*\[code\]([\s\S]*?)\[/code\]\s*\[/hide\]`)
	mediaInfoHideFontPattern   = regexp.MustCompile(`(?is)\[hide=([^\]]*mediainfo[^\]]*)\]\s*\[font=monospace\]([\s\S]*?)\[/font\]\s*\[/hide\]`)
	mediaInfoQuotePattern      = regexp.MustCompile(`(?is)\[quote=([^\]]*mediainfo[^\]]*)\]([\s\S]*?)\[/quote\]`)
	mediaInfoSectionPattern    = regexp.MustCompile(`(?i)^(general|video|audio|text)(?:\s*#\d+)?$`)
	mediaInfoStripPathPattern  = regexp.MustCompile(`[\\/]+`)
	mediaInfoWhitespacePattern = regexp.MustCompile(`\s+`)
)

func renderBBCodeWithMediaInfo(value string) (string, bool) {
	normalized := normalizeBBCode(value)
	var builder strings.Builder
	found := false
	offset := 0

	for offset < len(normalized) {
		block, ok := nextMediaInfoBlock(normalized[offset:])
		if !ok {
			break
		}
		block.start += offset
		block.end += offset
		if block.start > offset {
			builder.WriteString(renderBBCode(normalized[offset:block.start]))
		}
		rendered, ok := renderMediaInfoBlock(block.label, block.raw)
		if !ok {
			builder.WriteString(renderBBCode(normalized[block.start:block.end]))
		} else {
			builder.WriteString(rendered)
			found = true
		}
		offset = block.end
	}

	if !found {
		return "", false
	}
	if offset < len(normalized) {
		builder.WriteString(renderBBCode(normalized[offset:]))
	}
	return builder.String(), true
}

func nextMediaInfoBlock(value string) (mediaInfoBlock, bool) {
	var best mediaInfoBlock
	bestSet := false
	if match := mediaInfoBBCodePattern.FindStringSubmatchIndex(value); len(match) == 4 {
		best = mediaInfoBlock{
			start: match[0],
			end:   match[1],
			label: "MediaInfo",
			raw:   value[match[2]:match[3]],
		}
		bestSet = true
	}
	if match := mediaInfoSpoilerPattern.FindStringSubmatchIndex(value); len(match) == 6 {
		candidate := mediaInfoBlock{
			start: match[0],
			end:   match[1],
			label: value[match[2]:match[3]],
			raw:   value[match[4]:match[5]],
		}
		if !bestSet || candidate.start < best.start {
			best = candidate
			bestSet = true
		}
	}
	for _, candidate := range mediaInfoBlocksFromPattern(value, mediaInfoHideCodePattern, "MediaInfo") {
		if !bestSet || candidate.start < best.start {
			best = candidate
			bestSet = true
		}
	}
	for _, candidate := range mediaInfoBlocksFromPattern(value, mediaInfoHideFontPattern, "MediaInfo") {
		if !bestSet || candidate.start < best.start {
			best = candidate
			bestSet = true
		}
	}
	for _, candidate := range mediaInfoBlocksFromPattern(value, mediaInfoQuotePattern, "MediaInfo") {
		if !bestSet || candidate.start < best.start {
			best = candidate
			bestSet = true
		}
	}
	return best, bestSet
}

func mediaInfoBlocksFromPattern(value string, pattern *regexp.Regexp, fallbackLabel string) []mediaInfoBlock {
	matches := pattern.FindAllStringSubmatchIndex(value, -1)
	blocks := make([]mediaInfoBlock, 0, len(matches))
	for _, match := range matches {
		if len(match) != 6 {
			continue
		}
		label := fallbackLabel
		if match[2] >= 0 && match[3] >= 0 {
			label = strings.TrimSpace(value[match[2]:match[3]])
		}
		if label == "" {
			label = fallbackLabel
		}
		blocks = append(blocks, mediaInfoBlock{
			start: match[0],
			end:   match[1],
			label: label,
			raw:   value[match[4]:match[5]],
		})
	}
	return blocks
}

func renderMediaInfoBlock(label string, raw string) (string, bool) {
	summary, ok := parseMediaInfoSummary(label, raw)
	if !ok {
		return "", false
	}
	var builder strings.Builder
	builder.WriteString(`<div class="mediainfo-preview">`)
	builder.WriteString(`<section class="mediainfo">`)
	if summary.filename != "" {
		builder.WriteString(`<section class="mediainfo__filename"><h3>Filename</h3>`)
		builder.WriteString(escapeHTML(summary.filename))
		builder.WriteString(`</section>`)
	}
	if len(summary.general) > 0 {
		writeMediaInfoFields(&builder, "mediainfo__general", "General", []mediaInfoField{
			{label: "Format", value: firstMediaInfoValue(summary.general, "format")},
			{label: "Duration", value: firstMediaInfoValue(summary.general, "duration")},
			{label: "Bitrate", value: firstMediaInfoValue(summary.general, "overall bit rate", "overall bitrate", "overallbitrate", "bit rate", "bitrate")},
			{label: "Size", value: firstMediaInfoValue(summary.general, "file size", "filesize")},
		})
	}
	if len(summary.video) > 0 {
		builder.WriteString(`<section class="mediainfo__video"><h3>Video</h3>`)
		for idx, track := range summary.video {
			builder.WriteString(`<article><h4>#`)
			builder.WriteString(strconv.Itoa(idx + 1))
			builder.WriteString(`</h4><dl>`)
			writeMediaInfoField(&builder, "Format", mediaInfoVideoFormat(track))
			writeMediaInfoField(&builder, "Resolution", mediaInfoResolution(track))
			writeMediaInfoField(&builder, "Aspect ratio", firstMediaInfoValue(track, "display aspect ratio", "displayaspectratio"))
			writeMediaInfoField(&builder, "Frame rate", mediaInfoFrameRate(track))
			writeMediaInfoField(&builder, "Bit rate", firstMediaInfoValue(track, "bit rate", "bitrate", "nominal bit rate", "bitrate nominal"))
			if hdr := mediaInfoHDR(track); hdr != "" {
				writeMediaInfoField(&builder, "HDR", hdr)
			}
			builder.WriteString(`</dl></article>`)
		}
		builder.WriteString(`</section>`)
	}
	if len(summary.audio) > 0 {
		builder.WriteString(`<section class="mediainfo__audio"><h3>Audio</h3><dl>`)
		for idx, track := range summary.audio {
			writeMediaInfoField(&builder, strconv.Itoa(idx+1)+".", strings.Join(nonEmptyMediaInfoValues(
				firstMediaInfoValue(track, "language"),
				firstMediaInfoValue(track, "format"),
				firstMediaInfoValue(track, "channel(s)", "channels", "channel"),
				firstMediaInfoValue(track, "bit rate", "bitrate"),
				firstMediaInfoValue(track, "title"),
			), " / "))
		}
		builder.WriteString(`</dl></section>`)
	}
	if len(summary.text) > 0 {
		builder.WriteString(`<section class="mediainfo__subtitles"><h3>Subtitles</h3><ul>`)
		for _, track := range summary.text {
			value := strings.Join(nonEmptyMediaInfoValues(
				firstMediaInfoValue(track, "language"),
				firstMediaInfoValue(track, "format"),
				firstMediaInfoValue(track, "title"),
			), " / ")
			if value == "" {
				continue
			}
			builder.WriteString(`<li>`)
			builder.WriteString(escapeHTML(value))
			builder.WriteString(`</li>`)
		}
		builder.WriteString(`</ul></section>`)
	}
	builder.WriteString(`</section>`)
	builder.WriteString(`<details class="mediainfo__raw"><summary>Raw `)
	builder.WriteString(escapeHTML(summary.label))
	builder.WriteString(`</summary><pre><code>`)
	builder.WriteString(escapeHTML(summary.raw))
	builder.WriteString(`</code></pre></details></div>`)
	return builder.String(), true
}

func parseMediaInfoSummary(label string, raw string) (mediaInfoSummary, bool) {
	cleaned := strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " "))
	if cleaned == "" {
		return mediaInfoSummary{}, false
	}
	sections := parseMediaInfoSections(cleaned)
	if len(sections) == 0 {
		return mediaInfoSummary{}, false
	}
	summary := mediaInfoSummary{
		label: strings.TrimSpace(label),
		raw:   cleaned,
	}
	if summary.label == "" {
		summary.label = "MediaInfo"
	}
	knownFields := 0
	for _, section := range sections {
		if len(section.fields) == 0 {
			continue
		}
		knownFields += len(section.fields)
		switch section.name {
		case "general":
			if summary.general == nil {
				summary.general = section.fields
				summary.filename = stripMediaInfoPath(firstMediaInfoValue(section.fields, "complete name", "completename", "file name", "filename"))
			}
		case "video":
			summary.video = append(summary.video, section.fields)
		case "audio":
			summary.audio = append(summary.audio, section.fields)
		case "text":
			summary.text = append(summary.text, section.fields)
		}
	}
	if knownFields < 2 {
		return mediaInfoSummary{}, false
	}
	return summary, true
}

func parseMediaInfoSections(value string) []mediaInfoSection {
	var sections []mediaInfoSection
	var current *mediaInfoSection
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" {
			continue
		}
		if match := mediaInfoSectionPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			sections = append(sections, mediaInfoSection{name: strings.ToLower(match[1]), fields: make(map[string]string)})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			continue
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = normalizeMediaInfoKey(key)
		val = strings.TrimSpace(val)
		if key == "" || val == "" {
			continue
		}
		current.fields[key] = val
	}
	return sections
}

func normalizeMediaInfoKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = mediaInfoWhitespacePattern.ReplaceAllString(value, " ")
	return value
}

func firstMediaInfoValue(track map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(track[normalizeMediaInfoKey(key)]); value != "" {
			return value
		}
	}
	return ""
}

func mediaInfoVideoFormat(track map[string]string) string {
	format := firstMediaInfoValue(track, "format")
	bitDepth := firstMediaInfoValue(track, "bit depth", "bitdepth")
	if bitDepth == "" {
		return format
	}
	if format == "" {
		return bitDepth
	}
	return format + " (" + bitDepth + ")"
}

func mediaInfoResolution(track map[string]string) string {
	width := firstMediaInfoValue(track, "width")
	height := firstMediaInfoValue(track, "height")
	if width == "" || height == "" {
		return ""
	}
	return width + " x " + height
}

func mediaInfoFrameRate(track map[string]string) string {
	if strings.EqualFold(firstMediaInfoValue(track, "frame rate mode", "framerate mode"), "Variable") {
		return "VFR"
	}
	return firstMediaInfoValue(track, "frame rate", "framerate", "original frame rate")
}

func mediaInfoHDR(track map[string]string) string {
	hdrFormat := firstMediaInfoValue(track, "hdr format", "hdr format commercial", "hdr_format")
	transfer := firstMediaInfoValue(track, "transfer characteristics")
	primaries := firstMediaInfoValue(track, "color primaries")
	lowerHDR := strings.ToLower(hdrFormat)
	lowerTransfer := strings.ToLower(transfer)
	lowerPrimaries := strings.ToLower(primaries)
	var values []string
	if strings.Contains(lowerHDR, "hdr10+") || strings.Contains(lowerHDR, "smpte st 2094 app 4") {
		values = append(values, "HDR10+")
	} else if strings.Contains(lowerHDR, "hdr10") {
		values = append(values, "HDR10")
	}
	switch {
	case strings.Contains(lowerHDR, "dvhe.05"):
		values = append(values, "Dolby Vision Profile 5")
	case strings.Contains(lowerHDR, "dvhe.07"):
		values = append(values, "Dolby Vision Profile 7")
	case strings.Contains(lowerHDR, "dvhe.08"):
		values = append(values, "Dolby Vision Profile 8")
	}
	if len(values) == 0 {
		switch {
		case strings.Contains(lowerTransfer, "hlg"):
			values = append(values, "HLG")
		case strings.Contains(lowerTransfer, "pq") && strings.Contains(lowerPrimaries, "bt.2020"):
			values = append(values, "PQ10")
		case strings.Contains(lowerTransfer, "bt.2020"):
			values = append(values, "WCG")
		case strings.Contains(lowerTransfer, "bt.709"):
			values = append(values, "SDR")
		}
	}
	return strings.Join(values, ", ")
}

func writeMediaInfoFields(builder *strings.Builder, className string, heading string, fields []mediaInfoField) {
	builder.WriteString(`<section class="`)
	builder.WriteString(className)
	builder.WriteString(`"><h3>`)
	builder.WriteString(escapeHTML(heading))
	builder.WriteString(`</h3><dl>`)
	for _, field := range fields {
		writeMediaInfoField(builder, field.label, field.value)
	}
	builder.WriteString(`</dl></section>`)
}

func writeMediaInfoField(builder *strings.Builder, label string, value string) {
	if strings.TrimSpace(value) == "" {
		value = "Unknown"
	}
	builder.WriteString(`<dt>`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</dt><dd>`)
	builder.WriteString(escapeHTML(value))
	builder.WriteString(`</dd>`)
}

func nonEmptyMediaInfoValues(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func stripMediaInfoPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := mediaInfoStripPathPattern.Split(trimmed, -1)
	if len(parts) == 0 {
		return trimmed
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return trimmed
	}
	return last
}

func escapeHTML(value string) string {
	return stdhtml.EscapeString(strings.TrimSpace(value))
}
