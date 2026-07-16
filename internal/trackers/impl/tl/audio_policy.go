// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import "github.com/autobrr/upbrr/internal/trackers"

// AudioPolicy allows additional audio languages.
func (Definition) AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowBloat: true}
}
