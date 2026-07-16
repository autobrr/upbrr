// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import "github.com/autobrr/upbrr/internal/trackers"

// AudioPolicy allows Romanian as an additional audio language.
func (Definition) AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"romanian"}}
}
