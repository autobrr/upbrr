// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/languageutil"
)

func validateMediaInfoUniqueID(meta preparationstate.State, doc mediaInfoDoc) (string, bool) {
	if !requiresUniqueID(meta) {
		return "", true
	}
	for _, track := range doc.Media.Track {
		if !strings.EqualFold(trackString(track, "@type"), "General") {
			continue
		}
		uniqueID := trackString(track, "UniqueID", "UniqueID/String")
		if strings.TrimSpace(uniqueID) != "" {
			return uniqueID, true
		}
	}
	return "", false
}

func requiresUniqueID(meta preparationstate.State) bool {
	if strings.TrimSpace(meta.DiscType) != "" {
		return false
	}
	if len(meta.FileList) == 0 {
		return strings.EqualFold(filepath.Ext(meta.SourcePath), ".mkv")
	}
	for _, path := range meta.FileList {
		if strings.EqualFold(filepath.Ext(path), ".mkv") {
			return true
		}
	}
	return false
}

func extractMediaInfoLanguages(doc mediaInfoDoc) ([]string, []string) {
	var audio []string
	var subs []string

	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(strings.TrimSpace(trackString(track, "@type")))
		switch trackType {
		case "audio":
			if isCommentaryOrCompatibilityAudioValue(trackString(track, "Title", "Title_String", "Title_String2", "Title_String3")) {
				continue
			}
			language := normalizeLanguage(trackString(track, "Language", "Language_String", "Language_String2", "Language_String3"))
			if language != "" {
				audio = append(audio, language)
			}
		case "text":
			language := normalizeLanguage(trackString(track, "Language", "Language_String", "Language_String2", "Language_String3"))
			if language != "" {
				subs = append(subs, language)
			}
		}
	}

	return uniqueStrings(audio), uniqueStrings(subs)
}

func normalizeLanguage(value string) string {
	return languageutil.NormalizeLanguageDisplay(value)
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
