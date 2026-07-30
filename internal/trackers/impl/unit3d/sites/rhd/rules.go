// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly requires a known resolution of at least 720p and an NFO for
// scene releases. Adult-content failures remain waivable; German-audio failures
// are strict.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:      true,
		MinResolution:   "720p",
		Language:        &trackers.LanguageRule{Languages: []string{"german", "ger", "de", "deu", "gsw"}, RequireAudio: true},
		RequireSceneNFO: true,
	}
}
