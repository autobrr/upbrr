// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tik

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly permits disc uploads only.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{RequireDiscOnly: true} }
