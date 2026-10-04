// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strconv"
	"strings"
)

// RegionID resolves a country code or positive explicit tracker ID. Disc playback
// zones (A/B/C) and unknown names are not country IDs and return an empty value.
func RegionID(value string) string { return resolveCatalogID(value, unit3DRegionIDs) }

// DistributorID resolves a default publisher name or positive explicit tracker ID.
// Unknown names return an empty value rather than guessing a nearby publisher.
func DistributorID(value string) string { return resolveCatalogID(value, unit3DDistributorIDs) }

func resolveCatalogID(value string, catalog map[string]string) string {
	value = strings.TrimSpace(value)
	if id, err := strconv.Atoi(value); err == nil && id > 0 {
		return strconv.Itoa(id)
	}
	return catalog[strings.ToUpper(value)]
}

func resolveSiteCatalogID(value string, site func(string) string, fallback func(string) string) string {
	if id := resolveCatalogID(value, nil); id != "" {
		return id
	}
	if site != nil {
		if id := site(strings.TrimSpace(value)); id != "" {
			return resolveCatalogID(id, nil)
		}
	}
	return fallback(value)
}

// RegionID resolves this site's effective country ID, preserving explicit numeric inputs.
func (s SiteProfile) RegionID(value string) string {
	return resolveSiteCatalogID(value, s.ResolveRegionID, RegionID)
}

// DistributorID resolves this site's effective publisher ID, preserving explicit numeric inputs.
func (s SiteProfile) DistributorID(value string) string {
	return resolveSiteCatalogID(value, s.ResolveDistributorID, DistributorID)
}
