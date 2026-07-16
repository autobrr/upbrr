// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{
		BlockAdult:       true,
		AdultMessage:     "Porn is not allowed",
		RequireMovieOnly: true,
	}
}
