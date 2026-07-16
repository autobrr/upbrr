// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{
		BlockAdult:      true,
		MinResolution:   "720p",
		Language:        &ruletypes.LanguageRule{Languages: []string{"german", "ger", "de", "deu", "gsw"}, RequireAudio: true},
		RequireSceneNFO: true,
	}
}
