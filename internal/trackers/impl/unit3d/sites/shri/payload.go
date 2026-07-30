// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	if value := numericValue(req.Meta.Region); value != "" {
		data["region_id"] = value
	}
	if value := numericValue(req.Meta.Distributor); value != "" {
		data["distributor_id"] = value
	}
}

func numericValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return trimmed
}
