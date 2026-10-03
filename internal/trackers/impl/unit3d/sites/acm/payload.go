// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string, regionID, distributorID func(string) string) {
	if resolved := regionID(req.Meta.Region); resolved != "" {
		data["region_id"] = resolved
	}
	if resolved := distributorID(req.Meta.Distributor); resolved != "" {
		data["distributor_id"] = resolved
	}
	if req.Logger != nil {
		req.Logger.Debugf("trackers: tracker=ACM state=payload_prepared category_id=%s type_id=%s resolution_id=%s region_id=%s distributor_id=%s",
			numericValue(data["category_id"]), numericValue(data["type_id"]), numericValue(data["resolution_id"]),
			numericValue(data["region_id"]), numericValue(data["distributor_id"]))
		req.Logger.Tracef(
			"trackers: tracker=ACM state=payload_evidence source_bytes=%d subtitle_languages=%d description_bytes=%d mediainfo_bytes=%d bdinfo_bytes=%d",
			req.Meta.SourceSize,
			len(req.Meta.SubtitleLanguages),
			len(data["description"]),
			len(data["mediainfo"]),
			len(data["bdinfo"]),
		)
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
