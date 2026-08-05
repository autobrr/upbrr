// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	hdr := strings.ToUpper(strings.TrimSpace(req.Meta.HDR))
	data["dv"] = boolString(strings.Contains(hdr, "DV"))
	if strings.Contains(hdr, "HDR10+") {
		data["hdr10p"] = "1"
		return
	}
	if strings.Contains(hdr, "HDR") || strings.Contains(hdr, "HLG") {
		data["hdr"] = "1"
	}
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
