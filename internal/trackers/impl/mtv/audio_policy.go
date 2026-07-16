// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import "github.com/autobrr/upbrr/internal/trackers"

// AudioPolicy blocks foreign audio when English is the original language.
func (*Definition) AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{BlockEnglishOriginalWithForeign: true}
}
