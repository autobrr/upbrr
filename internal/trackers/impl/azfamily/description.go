// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	azTagStripPattern     = regexp.MustCompile(`(?is)\[/?(?:size|align|left|center|right|img|table|tr|td|spoiler|url)[^\]]*\]`)
	azNFOStripPattern     = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? NFO:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	azLinkStripPattern    = regexp.MustCompile(`https?://\S+|www\.\S+`)
	azPHDLimitedPattern   = regexp.MustCompile(`(?i)\bLIMITED\b`)
	azPHDCriterionPattern = regexp.MustCompile(`(?i)\bCriterion Collection\b`)
	azPHDAnnivPattern     = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary Edition\b`)
	azPHDDirCutPattern    = regexp.MustCompile("(?i)\\bDirector[’'`]s\\s+Cut\\b")
	azPHDExtCutPattern    = regexp.MustCompile(`(?i)\bExtended\s+Cut\b`)
	azPHDTheatrical       = regexp.MustCompile(`(?i)\bTheatrical\s+Cut\b`)
	azNoGroupPattern      = regexp.MustCompile(`(?i)-(?:nogrp|nogroup|unknown|unk)`)
)

func buildDescription(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
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
	return strings.Join(cleaned, "<br>\n")
}

func buildDescriptionFromAssets(_ context.Context, req trackers.PreparationInput) string {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		return ""
	}
	return buildDescription(assets.Description)
}

func editName(site siteDefinition, meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if name == "" {
		name = strings.TrimSpace(meta.Filename)
	}
	aka := ""
	if meta.ProviderMetadata.TMDB != nil {
		aka = strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle)
	}
	if aka != "" {
		name = strings.ReplaceAll(name, aka, "")
	}
	name = strings.ReplaceAll(name, "Dubbed", "")
	name = strings.ReplaceAll(name, "Dual-Audio", "")

	if site.Name == "PHD" {
		name = azPHDLimitedPattern.ReplaceAllString(name, "")
		name = azPHDCriterionPattern.ReplaceAllString(name, "")
		name = azPHDAnnivPattern.ReplaceAllString(name, "")
		name = azPHDDirCutPattern.ReplaceAllString(name, "DC")
		name = azPHDExtCutPattern.ReplaceAllString(name, "Extended")
		name = azPHDTheatrical.ReplaceAllString(name, "Theatrical")
	}

	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-"))
	if tag == "" || tag == "nogrp" || tag == "nogroup" || tag == "unknown" || tag == "unk" {
		name = azNoGroupPattern.ReplaceAllString(name, "")
		switch site.Name {
		case "CZ":
			name += "-NoGroup"
		case "PHD":
			name += "-NOGROUP"
		}
	}

	if isTV(meta) && meta.Release.Year > 0 {
		if site.Name == "PHD" {
			name = strings.ReplaceAll(name, strconv.Itoa(meta.Release.Year), "")
		} else if strings.TrimSpace(meta.Release.Title) != "" {
			name = strings.Replace(name, meta.Release.Title, meta.Release.Title+" "+strconv.Itoa(meta.Release.Year), 1)
		}
	}
	return strings.Join(strings.Fields(name), " ")
}
