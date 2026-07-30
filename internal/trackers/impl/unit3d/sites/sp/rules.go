// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly requires a known resolution of at least 1080p; its
// adult-content failure remains waivable.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:    true,
		AdultMessage:  "Porn is not allowed",
		MinResolution: "1080p",
	}
}
