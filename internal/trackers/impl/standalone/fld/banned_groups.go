// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"maps"
	"slices"
)

var fldBannedReleaseGroups = map[string]struct{}{
	"4K4U":       {},
	"AOC":        {},
	"C4K":        {},
	"CRUCiBLE":   {},
	"d3g":        {},
	"EASports":   {},
	"FGT":        {},
	"MeGusta":    {},
	"MezRips":    {},
	"nikt0":      {},
	"ProRes":     {},
	"RARBG":      {},
	"ReaLHD":     {},
	"SasukeducK": {},
	"Sicario":    {},
	"TEKNO3D":    {},
	"Telly":      {},
	"tigole":     {},
	"TOMMY":      {},
	"WKS":        {},
	"x0r":        {},
	"YIFY":       {},
}

func bannedGroups() []string {
	groups := slices.Collect(maps.Keys(fldBannedReleaseGroups))
	slices.Sort(groups)
	return groups
}

func isBannedReleaseGroup(group string) bool {
	_, banned := fldBannedReleaseGroups[group]
	return banned
}
