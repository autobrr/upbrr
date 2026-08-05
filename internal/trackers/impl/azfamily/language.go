// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func languageValues(meta api.UploadSubject) languageBundle {
	audioSet := make(map[string]struct{})
	for _, value := range meta.AudioLanguages {
		if id, ok := languageID(value); ok {
			audioSet[id] = struct{}{}
		}
	}
	subtitleSet := make(map[string]struct{})
	for _, value := range meta.SubtitleLanguages {
		if id, ok := languageID(value); ok {
			subtitleSet[id] = struct{}{}
		}
	}
	return languageBundle{
		Audio:     sortedKeys(audioSet),
		Subtitles: sortedKeys(subtitleSet),
	}
}

func languageID(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "english", "eng", "en":
		return "45", true
	case "japanese", "jpn", "ja":
		return "76", true
	case "korean", "kor", "ko":
		return "85", true
	case "chinese", "zho", "zh":
		return "33", true
	case "cantonese", "yue":
		return "27", true
	case "french", "fra", "fre", "fr":
		return "52", true
	case "german", "deu", "ger", "de":
		return "58", true
	case "spanish", "spa", "es":
		return "132", true
	case "italian", "ita", "it":
		return "74", true
	case "portuguese", "por", "pt":
		return "116", true
	case "russian", "rus", "ru":
		return "123", true
	case "thai", "tha", "th":
		return "137", true
	case "vietnamese", "vie", "vi":
		return "154", true
	case "indonesian", "ind", "id":
		return "72", true
	case "malay", "msa", "may", "ms":
		return "97", true
	case "tagalog", "tgl", "tl", "filipino":
		return "136", true
	case "hindi", "hin", "hi":
		return "66", true
	case "arabic", "ara", "ar":
		return "7", true
	case "turkish", "tur", "tr":
		return "145", true
	case "dutch", "nld", "dut", "nl":
		return "43", true
	case "polish", "pol", "pl":
		return "114", true
	case "danish", "dan", "da":
		return "41", true
	case "finnish", "fin", "fi":
		return "51", true
	case "swedish", "swe", "sv":
		return "134", true
	case "norwegian", "nor", "no", "nob", "nb":
		return "22", true
	case "hebrew", "heb", "he":
		return "64", true
	case "ukrainian", "ukr", "uk":
		return "147", true
	default:
		return "", false
	}
}
