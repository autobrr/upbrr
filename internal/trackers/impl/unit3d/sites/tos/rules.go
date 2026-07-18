// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tos

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly requires an NFO for scene releases and applies a waivable
// French audio-or-subtitle requirement that accepts original-language audio
// when French subtitles are present.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Language: &trackers.LanguageRule{
		Languages:     []string{"french", "fr", "fra", "fre"},
		RequireAudio:  true,
		RequireSubs:   true,
		AllowOriginal: true,
	}, RequireSceneNFO: true}
}
