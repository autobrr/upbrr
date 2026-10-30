// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"golang.org/x/text/language"

	"github.com/autobrr/upbrr/internal/languageutil"
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
	tag, ok := languageutil.LanguageTag(value)
	if !ok {
		return "", false
	}
	// This taxonomy groups aliases that remain distinct in canonical media facts.
	tag, _ = language.All.Canonicalize(tag)
	base, _, _ := tag.Raw()
	switch base.String() {
	case "en":
		return "45", true
	case "ja":
		return "76", true
	case "ko":
		return "85", true
	case "zh":
		return "33", true
	case "yue":
		return "27", true
	case "fr":
		return "52", true
	case "de":
		return "58", true
	case "es":
		return "132", true
	case "it":
		return "74", true
	case "pt":
		return "116", true
	case "ru":
		return "123", true
	case "th":
		return "137", true
	case "vi":
		return "154", true
	case "id":
		return "72", true
	case "ms":
		return "97", true
	case "fil":
		return "136", true
	case "hi":
		return "66", true
	case "ar":
		return "7", true
	case "tr":
		return "145", true
	case "nl":
		return "43", true
	case "pl":
		return "114", true
	case "da":
		return "41", true
	case "fi":
		return "51", true
	case "sv":
		return "134", true
	case "no", "nn":
		return "22", true
	case "he":
		return "64", true
	case "uk":
		return "147", true
	default:
		return "", false
	}
}
