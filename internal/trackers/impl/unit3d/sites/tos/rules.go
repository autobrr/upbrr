// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tos

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{Language: &ruletypes.LanguageRule{
		Languages:     []string{"french", "fr", "fra", "fre"},
		RequireAudio:  true,
		RequireSubs:   true,
		AllowOriginal: true,
	}, RequireSceneNFO: true}
}
