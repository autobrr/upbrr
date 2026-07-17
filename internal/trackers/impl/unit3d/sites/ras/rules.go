// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ras

import "github.com/autobrr/upbrr/internal/trackers"

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Language: &trackers.LanguageRule{
		Languages:    []string{"english", "norwegian", "norsk", "no", "nb", "nn", "swedish", "sv", "danish", "da", "finnish", "fi", "icelandic", "is"},
		RequireAudio: true,
		RequireSubs:  true,
	}}
}
