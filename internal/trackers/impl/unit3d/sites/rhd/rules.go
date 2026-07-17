// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import "github.com/autobrr/upbrr/internal/trackers"

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:      true,
		MinResolution:   "720p",
		Language:        &trackers.LanguageRule{Languages: []string{"german", "ger", "de", "deu", "gsw"}, RequireAudio: true},
		RequireSceneNFO: true,
	}
}
