// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import "github.com/autobrr/upbrr/internal/trackers"

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:    true,
		AdultMessage:  "Porn is not allowed",
		MinResolution: "1080p",
	}
}
