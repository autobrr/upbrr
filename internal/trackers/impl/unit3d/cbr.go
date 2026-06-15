// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var cbrCodecReplacer = strings.NewReplacer(
	"DD+ ", "DDP",
	"DD ", "DD",
	"AAC ", "AAC",
	"FLAC ", "FLAC",
)

var cbrAudioMarkerReplacer = strings.NewReplacer(
	"Dubbed", "",
	"Dual-Audio", "",
)

var audioTagRegex = regexp.MustCompile(`(?i)-([^.-]+)\.(?:DUAL|MULTI)`)

var ptbrMap = map[string]struct{}{
	"português": {}, "portuguese": {}, "pt-br": {}, "pt": {},
}

func siteCBRProfile() unit3DSiteProfile {
	return unit3DSiteProfile{resolveCategoryID: resolveUnit3DCBRCategoryID}
}

func resolveUnit3DCBRCategoryID(meta api.PreparedMetadata) string {
	if strings.EqualFold(resolveUnit3DCategory(meta), "TV") && meta.Anime {
		return "4"
	}
	return resolveUnit3DCategoryID(meta)
}

func BuildCBRName(meta api.PreparedMetadata, customTag string) string {
	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	name = cbrCodecReplacer.Replace(name)

	category := resolveUnit3DCategory(meta)
	if category == "TV" || meta.Anime {
		if meta.Release.Year > 0 {
			yearStr := strconv.Itoa(meta.Release.Year)
			if strings.Contains(name, yearStr) {
				name = strings.ReplaceAll(name, "("+yearStr+")", "")
				name = strings.ReplaceAll(name, yearStr, "")
				name = strings.TrimSpace(name)
			}
		}
	}

	origLang := strings.ToLower(resolveOriginalLanguage(meta))
	aka := ""
	if meta.ExternalMetadata.TMDB != nil {
		aka = meta.ExternalMetadata.TMDB.RetrievedAKA
	}

	_, isPtBR := ptbrMap[origLang]

	if !isPtBR && aka != "" {
		name = strings.ReplaceAll(name, aka, "")
		name = strings.Join(strings.Fields(name), " ")
	}

	if isPtBR && aka != "" {
		akaClean := strings.TrimSpace(strings.ReplaceAll(aka, "AKA", ""))
		title := meta.Release.Title

		name = strings.ReplaceAll(name, aka, "")
		name = strings.ReplaceAll(name, strings.ReplaceAll(aka, " ", "."), "")
		name = strings.ReplaceAll(name, title, akaClean)
		name = strings.ReplaceAll(name, strings.ReplaceAll(title, " ", "."), akaClean)
		name = strings.Join(strings.Fields(name), " ")
	}

	cbrName := name

	if !isDiscType(meta.DiscType) {
		audioTag := ""
		hasPortuguese := false
		for _, l := range meta.AudioLanguages {
			if _, ok := ptbrMap[strings.ToLower(l)]; ok {
				hasPortuguese = true
				break
			}
		}

		if hasPortuguese {
			uniqueLangs := make(map[string]struct{})
			for _, l := range meta.AudioLanguages {
				trimmed := strings.TrimSpace(l)
				if trimmed == "" {
					continue
				}
				if code, _, ok := languageCode(trimmed); ok {
					uniqueLangs[code] = struct{}{}
				}
			}

			count := len(uniqueLangs)
			if count >= 3 {
				audioTag = " MULTI"
			} else if count == 2 {
				audioTag = " DUAL"
			}
		}

		if audioTag != "" {
			cbrName = cbrAudioMarkerReplacer.Replace(cbrName)
			cbrName = strings.Join(strings.Fields(cbrName), " ")
			if idx := strings.LastIndex(cbrName, "-"); idx != -1 {
				prefix := cbrName[:idx]
				suffix := cbrName[idx+1:]

				cleanCustomTag := strings.TrimPrefix(customTag, "-")
				if cleanCustomTag != "" && strings.EqualFold(strings.TrimSpace(suffix), cleanCustomTag) {
					searchStr := meta.Filename
					if searchStr == "" {
						searchStr = meta.ReleaseName
					}

					if match := audioTagRegex.FindStringSubmatch(searchStr); len(match) > 1 {
						originalGroupTag := match[1]
						if !strings.EqualFold(originalGroupTag, meta.Release.Group) {
							cbrName = prefix + "-" + originalGroupTag + audioTag + "-" + suffix
						} else {
							cbrName = prefix + audioTag + "-" + suffix
						}
					} else {
						cbrName = prefix + audioTag + "-" + suffix
					}
				} else {
					cbrName = prefix + audioTag + "-" + suffix
				}
			} else {
				cbrName += audioTag
			}
		}
	}

	return addNoGroupSuffix(cbrName, meta, "NoGroup")
}
