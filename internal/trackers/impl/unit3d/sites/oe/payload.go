// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import "github.com/autobrr/upbrr/internal/trackers"

func additionalPayload(_ trackers.PreparationInput, data map[string]string) {
	if _, ok := data["tvdb"]; !ok {
		data["tvdb"] = "0"
	}
}
