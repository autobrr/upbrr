// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package znth

import "github.com/autobrr/upbrr/internal/trackers"

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{BlockAdult: true, AdultMessage: "Porn/xxx is not allowed at ZNTH."}
}
