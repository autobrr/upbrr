// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"html"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ManualLanguageBlock renders the manually supplied language facts for a
// generated BBCode description. Empty lists do not produce a line.
func ManualLanguageBlock(languages api.ManualLanguageFacts) string {
	lines := make([]string, 0, 3)
	for _, item := range []struct {
		label  string
		values []string
	}{
		{label: "Audio Language/s", values: languages.Audio},
		{label: "Subtitle Language/s", values: languages.Subtitles},
		{label: "Hardcoded Subtitle Language/s", values: languages.HardcodedSubtitles},
	} {
		values := escapedLanguageValues(item.values)
		if len(values) != 0 {
			lines = append(lines, item.label+": "+strings.Join(values, ", "))
		}
	}
	return strings.Join(lines, "\n")
}

func escapedLanguageValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		value = html.EscapeString(value)
		value = strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(value)
		result = append(result, value)
	}
	return result
}
