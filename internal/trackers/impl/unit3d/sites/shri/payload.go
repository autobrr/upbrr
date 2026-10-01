// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string, regionID, distributorID func(string) string) {
	if value := regionID(req.Meta.Region); value != "" {
		data["region_id"] = value
	}
	if value := distributorID(req.Meta.Distributor); value != "" {
		data["distributor_id"] = value
	}
}
