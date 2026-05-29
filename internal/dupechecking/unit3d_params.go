// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildUnit3DSearchParams(meta api.PreparedMetadata, tracker string) url.Values {
	tmdbID := meta.ExternalIDs.TMDBID
	if tmdbID == 0 {
		return nil
	}

	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}
	if category == "" && meta.ReleaseNameOverrides.Category != nil {
		category = strings.ToUpper(strings.TrimSpace(*meta.ReleaseNameOverrides.Category))
	}
	categoryID := resolveUnit3DDupeCategoryID(tracker, category)
	if categoryID == "" {
		return nil
	}

	typeValue := strings.ToUpper(strings.TrimSpace(meta.Type))
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	if typeValue == "" && strings.TrimSpace(meta.DiscType) != "" {
		typeValue = "DISC"
	}
	if typeValue == "" {
		if meta.ReleaseNameOverrides.Type != nil {
			typeValue = strings.ToUpper(strings.TrimSpace(*meta.ReleaseNameOverrides.Type))
		}
	}
	typeID := resolveUnit3DDupeTypeID(tracker, typeValue)

	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		if meta.ReleaseNameOverrides.Resolution != nil {
			resolution = strings.TrimSpace(*meta.ReleaseNameOverrides.Resolution)
		}
	}
	resolutionID := resolveUnit3DDupeResolutionID(tracker, resolution)
	if resolutionID == "" {
		resolutionID = resolveUnit3DDupeResolutionID(tracker, "8640p")
	}

	params := url.Values{}
	params.Set("tmdbId", strconv.Itoa(tmdbID))
	params.Set("categories[]", categoryID)
	params.Set("name", "")
	params.Set("perPage", "100")

	if !strings.EqualFold(tracker, "OTW") {
		if resolutionID == "3" || resolutionID == "4" {
			params.Add("resolutions[]", "3")
			params.Add("resolutions[]", "4")
		} else {
			params.Set("resolutions[]", resolutionID)
		}
	}

	if typeID != "" && !strings.EqualFold(tracker, "SP") && !strings.EqualFold(tracker, "STC") {
		params.Set("types[]", typeID)
	}

	if strings.EqualFold(category, "TV") {
		season := resolveSeasonValue(meta)
		if season != "" {
			params.Set("name", " "+season)
		}
	}

	return params
}

func resolveUnit3DDupeCategoryID(tracker string, category string) string {
	if strings.EqualFold(tracker, "EMUW") {
		switch strings.ToUpper(strings.TrimSpace(category)) {
		case "MOVIE", "FANRES":
			return "1"
		case "TV":
			return "2"
		default:
			return ""
		}
	}
	return trackerdata.CategoryID(category)
}

func resolveUnit3DDupeTypeID(tracker string, typeValue string) string {
	if strings.EqualFold(tracker, "EMUW") {
		mapping := map[string]string{
			"DISC":   "1",
			"REMUX":  "2",
			"ENCODE": "3",
			"WEBDL":  "4",
			"WEBRIP": "5",
			"HDTV":   "6",
			"SD":     "7",
		}
		return mapping[strings.ToUpper(strings.TrimSpace(typeValue))]
	}
	return trackerdata.TypeID(typeValue)
}

func resolveUnit3DDupeResolutionID(tracker string, resolution string) string {
	if strings.EqualFold(tracker, "EMUW") {
		mapping := map[string]string{
			"4320p": "1",
			"2160p": "2",
			"1080p": "3",
			"1080i": "4",
			"720p":  "5",
			"576p":  "6",
			"540p":  "7",
			"480p":  "8",
			"8640p": "10",
		}
		return mapping[strings.ToLower(strings.TrimSpace(resolution))]
	}
	return trackerdata.ResolutionID(resolution)
}

func resolveSeasonValue(meta api.PreparedMetadata) string {
	if meta.ReleaseNameOverrides.Season != nil {
		return normalizeSeasonEpisode(*meta.ReleaseNameOverrides.Season)
	}
	season, _ := parseSeasonEpisode(meta.ReleaseName)
	return season
}

func normalizeSeasonEpisode(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "S") {
		return upper
	}
	if num, err := strconv.Atoi(trimmed); err == nil {
		return "S" + strconv.Itoa(num)
	}
	return upper
}
