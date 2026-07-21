// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly blocks DVDRip uploads.
func Rules() *trackers.RuleSet { return &trackers.RuleSet{BlockDVDRip: true} }
