// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mns

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns MNS's waivable adult-content prohibition.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{BlockAdult: true, AdultMessage: "Adult content is not allowed"}
}
