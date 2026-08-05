// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

// Rules strictly enforces valid MediaInfo settings, the DVDRip prohibition,
// and the shared language requirement.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockDVDRip:           true,
		Language: &trackers.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			ApplyIfNonDisc: true,
		},
	}
}
