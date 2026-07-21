// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lt

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns LT's strict Spanish audio-or-subtitle requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		Language: &trackers.LanguageRule{
			Languages:    []string{"spanish", "es", "spa"},
			RequireAudio: true,
			RequireSubs:  true,
		},
	}
}
