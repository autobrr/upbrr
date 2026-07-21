// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package stc

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly rejects known non-TV categories; its adult-content failure
// remains waivable.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:    true,
		AdultMessage:  "Porn is not allowed",
		RequireTVOnly: true,
	}
}
