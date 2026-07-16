// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ras

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{Language: &ruletypes.LanguageRule{
		Languages:    []string{"english", "norwegian", "norsk", "no", "nb", "nn", "swedish", "sv", "danish", "da", "finnish", "fi", "icelandic", "is"},
		RequireAudio: true,
		RequireSubs:  true,
	}}
}
