// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
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
			audio = append(audio, mediaInfoTrackLanguages(track)...)
		case "text":
			subs = append(subs, mediaInfoTrackLanguages(track)...)
		}
	}

	return languageutil.NormalizeLanguageList(audio), languageutil.NormalizeLanguageList(subs)
}

func mediaInfoTrackLanguages(track map[string]any) []string {
	if code := trackString(track, "Language"); code != "" {
		return languageutil.NormalizeLanguageList([]string{code})
	}
	return languageutil.NormalizeLanguageNames([]string{trackString(track, "Language_String", "Language_String2", "Language_String3")})
}

// extractBDInfoLanguages returns normalized, case-insensitively deduplicated
// audio and subtitle languages from a parsed quick summary.
func extractBDInfoLanguages(info *discparse.BDInfo) ([]string, []string) {
	if info == nil {
		return nil, nil
	}
	audio := make([]string, 0, len(info.Audio))
	for _, track := range info.Audio {
		if isBDInfoCommentary(track) {
			continue
		}
		audio = append(audio, track.Language)
	}
	return languageutil.NormalizeLanguageNames(audio), languageutil.NormalizeLanguageNames(info.Subtitles)
}
