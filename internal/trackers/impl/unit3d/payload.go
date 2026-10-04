// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

// applyDiscTaxonomy adds optional disc metadata before site-specific payload
// handling. Unknown names are omitted; site validation may still require them.
func applyDiscTaxonomy(req trackers.PreparationInput, data map[string]string, profile SiteProfile) {
	if !isDiscType(req.Meta.DiscType) {
		return
	}
	set := func(key, value string, resolve func(string) string) {
		id := resolve(value)
		if id != "" {
			data[key] = id
		}
		if req.Logger != nil && strings.TrimSpace(value) != "" {
			decision := "mapped"
			if id == "" {
				decision = "omitted_unknown"
			}
			req.Logger.Debugf("trackers: disc taxonomy tracker=%s field=%s decision=%s id=%s", req.Tracker, key, decision, id)
		}
	}
	set("region_id", req.Meta.Region, profile.RegionID)
	set("distributor_id", req.Meta.Distributor, profile.DistributorID)
}
