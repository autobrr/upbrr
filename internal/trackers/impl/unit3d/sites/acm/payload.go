// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	if regionID := numericValue(req.Meta.Region); regionID != "" {
		data["region_id"] = regionID
	}
	if distributorID := numericValue(req.Meta.Distributor); distributorID != "" {
		data["distributor_id"] = distributorID
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
