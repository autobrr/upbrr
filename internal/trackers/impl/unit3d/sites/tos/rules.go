// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tos

import "github.com/autobrr/upbrr/internal/trackers"

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Language: &trackers.LanguageRule{
		Languages:     []string{"french", "fr", "fra", "fre"},
		RequireAudio:  true,
		RequireSubs:   true,
		AllowOriginal: true,
	}, RequireSceneNFO: true}
}
