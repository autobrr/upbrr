// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var localizedCodecReplacer = strings.NewReplacer("DD+ ", "DDP", "DD ", "DD", "AAC ", "AAC", "FLAC ", "FLAC")
var localizedAudioMarkerReplacer = strings.NewReplacer("Dubbed", "", "Dual-Audio", "")
var localizedAudioTagRegex = regexp.MustCompile(`(?i)-([^.-]+)\.(?:DUAL|MULTI)`)
var portugueseLanguageNames = map[string]struct{}{
	"português":  {},
	"portuguese": {},
	"pt-br":      {},
	"pt":         {},
}

// FormatLocalizedName applies the shared Portuguese-localized naming convention
// used by Unit3D sites that opt into it.
func FormatLocalizedName(meta api.UploadSubject, customTag string) string {
	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}
	name = localizedCodecReplacer.Replace(name)
	category := resolveUnit3DCategory(meta)
	if (category == "TV" || meta.Anime) && meta.Release.Year > 0 {
		year := strconv.Itoa(meta.Release.Year)
		if strings.Contains(name, year) {
			name = strings.ReplaceAll(name, "("+year+")", "")
			name = strings.ReplaceAll(name, year, "")
			name = strings.TrimSpace(name)
		}
	}
	originalLanguage := strings.ToLower(resolveOriginalLanguage(meta))
	aka := ""
	if meta.ProviderMetadata.TMDB != nil {
		aka = meta.ProviderMetadata.TMDB.RetrievedAKA
	}
	_, portuguese := portugueseLanguageNames[originalLanguage]
	if !portuguese && aka != "" {
		name = strings.ReplaceAll(name, aka, "")
		name = strings.Join(strings.Fields(name), " ")
	}
	if portuguese && aka != "" {
		cleanAKA := strings.TrimSpace(strings.ReplaceAll(aka, "AKA", ""))
		title := meta.Release.Title
		name = strings.ReplaceAll(name, aka, "")
		name = strings.ReplaceAll(name, strings.ReplaceAll(aka, " ", "."), "")
		name = strings.ReplaceAll(name, title, cleanAKA)
		name = strings.ReplaceAll(name, strings.ReplaceAll(title, " ", "."), cleanAKA)
		name = strings.Join(strings.Fields(name), " ")
	}
	formatted := name
	if !isDiscType(meta.DiscType) {
		audioTag, hasPortuguese := "", false
		for _, language := range meta.AudioLanguages {
			if _, ok := portugueseLanguageNames[strings.ToLower(language)]; ok {
				hasPortuguese = true
				break
			}
		}
		if hasPortuguese {
			languages := make(map[string]struct{})
			for _, language := range meta.AudioLanguages {
				trimmed := strings.TrimSpace(language)
				if trimmed == "" {
					continue
				}
				if code, _, ok := languageCode(trimmed); ok {
					languages[code] = struct{}{}
				}
			}
			if len(languages) >= 3 {
				audioTag = " MULTI"
			} else if len(languages) == 2 {
				audioTag = " DUAL"
			}
		}
		if audioTag != "" {
			formatted = localizedAudioMarkerReplacer.Replace(formatted)
			formatted = strings.Join(strings.Fields(formatted), " ")
			if index := strings.LastIndex(formatted, "-"); index != -1 {
				prefix, suffix := formatted[:index], formatted[index+1:]
				cleanTag := strings.TrimPrefix(customTag, "-")
				if cleanTag != "" && strings.EqualFold(strings.TrimSpace(suffix), cleanTag) {
					search := meta.Filename
					if search == "" {
						search = meta.ReleaseName
					}
					if match := localizedAudioTagRegex.FindStringSubmatch(search); len(match) > 1 {
						group := match[1]
						if !strings.EqualFold(group, meta.Release.Group) {
							formatted = prefix + "-" + group + audioTag + "-" + suffix
						} else {
							formatted = prefix + audioTag + "-" + suffix
						}
					} else {
						formatted = prefix + audioTag + "-" + suffix
					}
				} else {
					formatted = prefix + audioTag + "-" + suffix
				}
			} else {
				formatted += audioTag
			}
		}
	}
	return addNoGroupSuffix(formatted, meta, "NoGroup")
}
