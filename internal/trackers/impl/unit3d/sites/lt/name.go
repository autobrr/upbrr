// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lt

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	markerPattern         = regexp.MustCompile(`\b(?:1080p|1080i|720p|2160p|4320p|8640p|mkv|mp4|avi|ts|bluray|blu-ray|web-dl|webdl|webrip|hdtv|dvd|complete|vostfr|multi|subfrench|french|truefrench)\b`)
	akaPattern            = regexp.MustCompile(`(?i)\baka\b`)
	multipleDotsPattern   = regexp.MustCompile(`\.+`)
	multipleSpacesPattern = regexp.MustCompile(`\s+`)
	trailingSuffixPattern = regexp.MustCompile(`(?i)\b(?:s\d+e\d+|s\d+|e\d+|\d{4})\b.*$`)
)

var audioLatinoCheck = map[string]bool{
	"lat": true, "es-419": true, "es-mx": true,
}

var audioCastilianCheck = map[string]bool{
	"castilian": true, "es-es": true,
}

var latinoKeywords = []string{
	"latino", "lat", "es-419", "mexicano", "argentino", "chileno", "colombiano", "peruano", "venezolano",
}

var castilianKeywords = []string{
	"castellano", "castilian", "es-es", "español de españa", "espanol de espana",
}

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	aka := ""
	title := ""
	origTitle := ""
	origLang := ""
	if meta.ProviderMetadata.TMDB != nil {
		aka = meta.ProviderMetadata.TMDB.RetrievedAKA
		title = meta.ProviderMetadata.TMDB.Title
		origTitle = meta.ProviderMetadata.TMDB.OriginalTitle
		origLang = meta.ProviderMetadata.TMDB.OriginalLanguage
	}
	if title == "" && meta.ProviderMetadata.IMDB != nil {
		title = meta.ProviderMetadata.IMDB.Title
	}
	if origTitle == "" {
		origTitle = title
	}

	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}

	// 1. Strip episode title for LT
	category := unit3d.Category(meta)
	if category == "TV" && meta.EpisodeTitle != "" {
		name = removeEpisodeTitle(name, meta.EpisodeTitle)
	}

	ltName := name
	ltName = strings.ReplaceAll(ltName, "Dual-Audio", "")
	ltName = strings.ReplaceAll(ltName, "Dubbed", "")

	// Find the end of the title block (start of year, resolution, source, etc.)
	titleEndIdx := len(ltName)
	if loc := markerPattern.FindStringIndex(strings.ToLower(ltName)); loc != nil {
		titleEndIdx = loc[0]
	}

	titleBlock := ltName[:titleEndIdx]
	restOfName := ltName[titleEndIdx:]

	// Preserve trailing season/episode/year in titleBlock
	cleanTitleBlock := titleBlock
	var trailingSuffix string
	if loc := trailingSuffixPattern.FindStringIndex(titleBlock); loc != nil {
		trailingSuffix = titleBlock[loc[0]:]
		cleanTitleBlock = titleBlock[:loc[0]]
	}

	// Determine the correct target title to use
	targetTitle := title
	if origLang == "es" {
		// Use Spanish title for Spanish original series/movies
		if aka != "" {
			akaClean := strings.TrimSpace(strings.ReplaceAll(aka, "AKA", ""))
			if akaClean != "" {
				targetTitle = akaClean
			}
		} else if origTitle != "" {
			targetTitle = origTitle
		}
	}

	if targetTitle != "" {
		targetTitleDotted := strings.ReplaceAll(targetTitle, " ", ".")
		cleanTitleBlockNorm := strings.Trim(strings.ReplaceAll(cleanTitleBlock, ".", " "), " ")
		// If the title block contains AKA, or we need to replace a mismatched title:
		if akaPattern.MatchString(cleanTitleBlock) || !strings.EqualFold(cleanTitleBlockNorm, targetTitle) {
			cleanTitleBlock = targetTitleDotted
		}
	}

	titleBlock = cleanTitleBlock + "." + trailingSuffix
	ltName = titleBlock + restOfName

	isDisc := false
	if unit3d.IsDiscType(meta.DiscType) || unit3d.IsDiscType(meta.Release.Type) || meta.DiscType != "" {
		isDisc = true
	}

	if !isDisc {
		type rawMediaInfoDoc struct {
			Media struct {
				Track []map[string]any `json:"track"`
			} `json:"media"`
		}

		var tracks []map[string]any
		if meta.MediaInfoJSONPath != "" {
			if payload, err := os.ReadFile(meta.MediaInfoJSONPath); err == nil {
				var doc rawMediaInfoDoc
				if err := json.Unmarshal(payload, &doc); err == nil {
					tracks = doc.Media.Track
				}
			}
		}

		hasSpanishAudio := false
		hasLatino := false
		hasCastilian := false

		hasTracks := false
		if len(tracks) > 0 {
			for _, track := range tracks {
				trackType := ""
				if val, ok := track["@type"]; ok {
					trackType = strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", val)))
				}
				if trackType != "audio" {
					continue
				}
				hasTracks = true
				lang := strings.ToLower(namesTrackString(track, "Language", "Language_String", "Language_String2", "Language_String3"))
				titleText := strings.ToLower(namesTrackString(track, "Title", "Title_String", "Title_String2", "Title_String3"))

				if strings.Contains(titleText, "commentary") {
					continue
				}

				isLatinoTitle := false
				for _, kw := range latinoKeywords {
					if strings.Contains(titleText, kw) {
						isLatinoTitle = true
						break
					}
				}
				isCastilianTitle := false
				for _, kw := range castilianKeywords {
					if strings.Contains(titleText, kw) {
						isCastilianTitle = true
						break
					}
				}

				if audioLatinoCheck[lang] || (lang == "es" && isLatinoTitle) {
					hasLatino = true
					hasSpanishAudio = true
				} else if (lang == "es" && isCastilianTitle) || audioCastilianCheck[lang] {
					hasCastilian = true
					hasSpanishAudio = true
				}
			}
		}

		if !hasTracks {
			// Fallback to AudioLanguages if MediaInfo JSON wasn't parsed
			for _, lang := range meta.AudioLanguages {
				if strings.EqualFold(lang, "Spanish") || strings.EqualFold(lang, "es") {
					hasSpanishAudio = true
					hasLatino = true
					break
				}
			}
		}

		tag := strings.TrimSpace(meta.Tag)
		// insertTagBracket inserts the label just before "-<tag>" so the result
		// is "… [LABEL]-TAG" rather than "…- [LABEL]TAG".
		insertTagBracket := func(s, label string) string {
			if tag != "" {
				sep := "-" + tag
				if idx := strings.LastIndex(s, sep); idx != -1 {
					return s[:idx] + " [" + label + "]" + s[idx:]
				}
			}
			return s + " [" + label + "]"
		}
		if hasSpanishAudio {
			if !hasLatino && hasCastilian {
				if !strings.Contains(ltName, "[CAST]") {
					ltName = insertTagBracket(ltName, "CAST")
				}
			}
		} else {
			if !strings.Contains(ltName, "[SUBS]") {
				ltName = insertTagBracket(ltName, "SUBS")
			}
		}
	}

	ltName = multipleDotsPattern.ReplaceAllString(ltName, ".")
	ltName = multipleSpacesPattern.ReplaceAllString(ltName, " ")
	return strings.Trim(ltName, ". ")
}

func removeEpisodeTitle(name string, episodeTitle string) string {
	cleanedTitle := strings.TrimSpace(episodeTitle)
	if cleanedTitle == "" {
		return name
	}

	// Remove with dots
	dotted := strings.ReplaceAll(cleanedTitle, " ", ".")
	name = strings.ReplaceAll(name, dotted, "")

	// Remove with spaces
	name = strings.ReplaceAll(name, cleanedTitle, "")

	// Clean up any duplicates
	name = strings.ReplaceAll(name, "..", ".")
	name = strings.ReplaceAll(name, "  ", " ")
	name = strings.ReplaceAll(name, " .", ".")
	name = strings.ReplaceAll(name, ". ", ".")

	return strings.Trim(name, ". ")
}

func namesTrackString(track map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := track[key]; ok {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", value))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
