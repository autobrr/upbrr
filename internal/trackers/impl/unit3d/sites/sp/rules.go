// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import "github.com/autobrr/upbrr/internal/trackers"

// Rules leaves evidence-backed release checks to the
// versioned validation policy.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{} }
