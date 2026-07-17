// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
)

var (
	hdbComparisonSectionPattern = regexp.MustCompile(`(?i)\[center\]\s*\[b\].*?(Comparison|vs).*?\[/b\][\s\S]*?\[/center\]`)
	hdbComparisonLinePattern    = regexp.MustCompile(`(?i)(.*comparison.*)\n`)
	hdbEmptyURLPattern          = regexp.MustCompile(`(?i)\[url=https?://(img\.|t\.)?hdbits\.org[^\]]*\]\[/url\]`)
	hdbEmptyAnyURLPattern       = regexp.MustCompile(`(?is)\[url\s*=\s*]\s*\[/url\]`)
	hdbURLTagPattern            = regexp.MustCompile(`(?i)(\[url[=]]https?://(img\.|t\.)?hdbits\.org[^\]]+\])(.*?)(\[/url\])?`)
	hdbImgPattern               = regexp.MustCompile(`(?i)\[img\][\s\S]*?(img\.|t\.)?hdbits\.org[\s\S]*?\[/img\]`)
	hdbStandalonePattern        = regexp.MustCompile(`(?i)https?://(img\.|t\.)?hdbits\.org/[^\s\[\]]+`)
	hdbURLTagAnyPattern         = regexp.MustCompile(`(?i)\[url[^\]]*hdbits\.org[^\]]*\](.*?)\[/url\]`)
	hdbSelfClosingPattern       = regexp.MustCompile(`(?i)\[url=https?://[^\]]*hdbits\.org[^\]]*\]\[/url\]`)
	hdbCenterEmptyPattern       = regexp.MustCompile(`(?i)\[center\]\s*\[/center\]`)
	hdbBBCodeURLImgPattern      = regexp.MustCompile(`(?i)\[url=(https?://[^\]]+)\]\[img\](https?://[^\]]+)\[/img\]\[/url\]`)
)

func CleanDescription(description string) bbcode.Report {
	desc := bbcode.NormalizeNewlines(description)
	imagelist := make([]bbcode.Image, 0)

	sections := hdbComparisonSectionPattern.FindAllString(desc, -1)
	for _, section := range sections {
		if strings.Contains(strings.ToLower(section), "hdbits.org") {
			desc = strings.ReplaceAll(desc, section, "")
		}
	}

	lines := hdbComparisonLinePattern.FindAllStringIndex(desc, -1)
	for _, match := range lines {
		start := match[0]
		end := min(start+500, len(desc))
		segment := desc[start:end]
		parts := strings.SplitN(segment, "\n", 4)
		if len(parts) > 3 {
			parts = parts[:3]
		}
		nextLines := strings.Join(parts, "\n")
		if strings.Contains(strings.ToLower(nextLines), "hdbits.org") {
			desc = strings.ReplaceAll(desc, nextLines, "")
		}
	}

	desc = hdbEmptyURLPattern.ReplaceAllString(desc, "")
	urlTags := hdbURLTagPattern.FindAllString(desc, -1)
	for _, urlTag := range urlTags {
		desc = strings.ReplaceAll(desc, urlTag, "")
	}

	imgTags := hdbImgPattern.FindAllString(desc, -1)
	for _, imgTag := range imgTags {
		desc = strings.ReplaceAll(desc, imgTag, "")
	}

	standalone := hdbStandalonePattern.FindAllString(desc, -1)
	for _, url := range standalone {
		desc = strings.ReplaceAll(desc, url, "")
	}

	desc = hdbURLTagAnyPattern.ReplaceAllString(desc, "")
	desc = hdbSelfClosingPattern.ReplaceAllString(desc, "")
	desc = hdbEmptyAnyURLPattern.ReplaceAllString(desc, "")
	desc = hdbComparisonSectionPattern.ReplaceAllString(desc, "")
	desc = hdbCenterEmptyPattern.ReplaceAllString(desc, "")
	desc = bbcode.RemoveExtraLines(desc)

	desc = hdbBBCodeURLImgPattern.ReplaceAllStringFunc(desc, func(value string) string {
		parts := hdbBBCodeURLImgPattern.FindStringSubmatch(value)
		if len(parts) < 3 {
			return value
		}
		webURL := parts[1]
		imgURL := parts[2]
		lower := strings.ToLower(webURL + imgURL)
		if strings.Contains(lower, "hdbits.org") {
			return ""
		}
		rawURL := imgURL
		rawURL = bbcode.NormalizeImageRawURL(rawURL)
		host := imagehost.ExtractHost(imgURL)
		imagelist = append(imagelist, bbcode.Image{
			ImgURL: imgURL,
			RawURL: rawURL,
			WebURL: webURL,
			Host:   host,
		})
		return ""
	})

	desc = strings.TrimSpace(desc)
	if desc == "" || bbcode.IsOnlyTags(desc) {
		return bbcode.Report{Images: imagelist}
	}
	return bbcode.Report{Description: desc, Images: imagelist}
}
