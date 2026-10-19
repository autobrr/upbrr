// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"
)

var (
	azTagStripPattern       = regexp.MustCompile(`(?is)\[/?(?:size|align|left|center|right|img|table|tr|td|spoiler|url)[^\]]*\]`)
	azNFOStripPattern       = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? NFO:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	azLinkStripPattern      = regexp.MustCompile(`https?://\S+|www\.\S+`)
	azCollectionLinkPattern = regexp.MustCompile(`\[url=https://slow\.pics/c/[A-Za-z0-9_-]{1,128}\][^\[\]]+\[/url\]`)
)

func buildDescription(raw string) string {
	trimmed := trackers.StripDescriptionSignatures(raw)
	if trimmed == "" {
		return ""
	}
	trimmed, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(trimmed)
	marker := "UPBRRCOLLECTIONLINK"
	for strings.Contains(trimmed, marker) {
		marker += "_"
	}
	var collectionLinks []string
	trimmed = description.MapOutsideLiteralAndHTMLTags(trimmed, func(fragment string, restore func(string) string) string {
		return azCollectionLinkPattern.ReplaceAllStringFunc(fragment, func(link string) string {
			collectionLinks = append(collectionLinks, restore(link))
			return marker + strconv.Itoa(len(collectionLinks)) + "END"
		})
	})
	trimmed = azNFOStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azLinkStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azTagStripPattern.ReplaceAllString(trimmed, "")
	escaped := html.EscapeString(strings.TrimSpace(trimmed))
	lines := strings.Split(escaped, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	base := strings.Join(cleaned, "<br>\n")
	for index, link := range collectionLinks {
		base = strings.ReplaceAll(base, marker+strconv.Itoa(index+1)+"END", description.Render(link))
	}
	if audioAnalysis == "" {
		return base
	}
	return strings.TrimSpace(strings.Join([]string{base, description.Render(audioAnalysis)}, "<br>\n"))
}

func buildDescriptionFromAssets(_ context.Context, req trackers.PreparationInput) string {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		return ""
	}
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return buildDescription(assets.Description)
}
