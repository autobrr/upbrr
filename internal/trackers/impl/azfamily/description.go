// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	azTagStripPattern      = regexp.MustCompile(`(?is)\[/?(?:size|align|left|center|right|img|table|tr|td|spoiler|url)[^\]]*\]`)
	azNFOStripPattern      = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? NFO:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	azLinkStripPattern     = regexp.MustCompile(`https?://\S+|www\.\S+`)
	azPHDLimitedPattern    = regexp.MustCompile(`(?i)\bLIMITED\b`)
	azPHDCriterionPattern  = regexp.MustCompile(`(?i)\bCriterion Collection\b`)
	azPHDAnnivPattern      = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary Edition\b`)
	azPHDDirCutPattern     = regexp.MustCompile("(?i)\\bDirector[’'`]s\\s+Cut\\b")
	azPHDExtCutPattern     = regexp.MustCompile(`(?i)\bExtended\s+Cut\b`)
	azPHDTheatrical        = regexp.MustCompile(`(?i)\bTheatrical\s+Cut\b`)
	azNoGroupPattern       = regexp.MustCompile(`(?i)-(?:nogrp|nogroup|unknown|unk)`)
	azEmptyParensPattern   = regexp.MustCompile(`\(\s*\)`)
	azEmptyBracketsPattern = regexp.MustCompile(`\[\s*\]`)
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

func buildDescriptionFromAssets(ctx context.Context, req trackers.UploadRequest) string {
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		return ""
	}
	return buildDescription(assets.Description)
}

// https://avistaz.to/guides/how-to-properly-titlename-a-torrent
// https://cinemaz.to/guides/how-to-properly-titlename-a-torrent
// https://privatehd.to/rules/upload-rules
func editName(site siteDefinition, meta api.PreparedMetadata) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if name == "" {
		name = strings.TrimSpace(meta.Filename)
	}

	aka := ""
	if meta.ExternalMetadata.TMDB != nil {
		aka = strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalTitle)
	}
	if aka == "" && meta.ExternalMetadata.IMDB != nil {
		aka = strings.TrimSpace(meta.ExternalMetadata.IMDB.AKA)
	}

	title := strings.TrimSpace(meta.Release.Title)

	if aka != "" && title != "" && !strings.EqualFold(aka, title) && strings.Contains(strings.ToLower(name), strings.ToLower(title)) {
		escapedAka := regexp.QuoteMeta(aka)
		re := regexp.MustCompile(`(?i)\b` + escapedAka + `\b`)
		name = re.ReplaceAllString(name, "")
	}
	name = strings.ReplaceAll(name, "Dubbed", "")
	name = strings.ReplaceAll(name, "Dual-Audio", "")

	if meta.EpisodeTitle != "" && (title == "" || !strings.EqualFold(meta.EpisodeTitle, title)) {
		name = strings.ReplaceAll(name, meta.EpisodeTitle, "")
	}
	if meta.DailyEpisodeDate != "" {
		name = strings.ReplaceAll(name, meta.DailyEpisodeDate, "")
	}

	name = azEmptyParensPattern.ReplaceAllString(name, "")
	name = azEmptyBracketsPattern.ReplaceAllString(name, "")

	if site.Name == "PHD" {
		name = azPHDLimitedPattern.ReplaceAllString(name, "")
		name = azPHDCriterionPattern.ReplaceAllString(name, "")
		name = azPHDAnnivPattern.ReplaceAllString(name, "")
		name = strings.Join(strings.Fields(name), " ")

		name = azPHDDirCutPattern.ReplaceAllString(name, "DC")
		name = azPHDExtCutPattern.ReplaceAllString(name, "Extended")
		name = azPHDTheatrical.ReplaceAllString(name, "Theatrical")
	}

	if meta.HasEncodeSettings {
		name = strings.ReplaceAll(name, "H.264", "x264")
		name = strings.ReplaceAll(name, "H.265", "x265")
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

	if isTV(meta) {
		yearToUse := meta.Release.Year
		seasonYear := 0
		if meta.SeasonInt > 0 && meta.ExternalMetadata.IMDB != nil {
			for _, s := range meta.ExternalMetadata.IMDB.SeasonsSummary {
				if s.Season == meta.SeasonInt {
					seasonYear = s.Year
					break
				}
			}
		}

		if seasonYear > 0 {
			yearToUse = seasonYear
		}

		if yearToUse > 0 {
			if site.Name == "PHD" {
				name = strings.ReplaceAll(name, strconv.Itoa(yearToUse), "")
			} else if title := strings.TrimSpace(meta.Release.Title); title != "" {
				name = strings.Replace(name, title, title+" "+strconv.Itoa(yearToUse), 1)
			}

			if site.Name == "AZ" && meta.TVPack {
				season := meta.SeasonStr
				if season == "" && meta.SeasonInt > 0 {
					season = fmt.Sprintf("S%02d", meta.SeasonInt)
				}
				if season != "" && meta.Release.Title != "" {
					oldStr := fmt.Sprintf("%s %d %s", meta.Release.Title, yearToUse, season)
					newStr := fmt.Sprintf("%s %s %d", meta.Release.Title, season, yearToUse)
					name = strings.ReplaceAll(name, oldStr, newStr)
				}
			}
		}
	}

	source := strings.TrimSpace(meta.Source)
	audio := strings.TrimSpace(meta.Audio)
	if strings.EqualFold(meta.Type, "DVDRIP") && source != "" {
		name = strings.ReplaceAll(name, source, "")
	}

	if strings.EqualFold(meta.DiscType, "DVD") {
		region := strings.TrimSpace(meta.Region)
		resolution := strings.TrimSpace(meta.Release.Resolution)
		videoCodec := strings.TrimSpace(meta.VideoCodec)

		if region != "" {
			name = strings.ReplaceAll(name, region, "")
		}
		if source != "" && resolution != "" {
			name = strings.ReplaceAll(name, source, resolution)
		}
		if audio != "" {
			suffix := ""
			if videoCodec != "" {
				suffix = " " + videoCodec
			}
			name = strings.ReplaceAll(name, audio, audio+suffix)
		}
	}

	return strings.Join(strings.Fields(name), " ")
}
