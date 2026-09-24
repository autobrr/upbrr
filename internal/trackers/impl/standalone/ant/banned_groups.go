// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"maps"
	"slices"
)

var antBannedReleaseGroups = map[string]struct{}{
	"4K4U":        {},
	"AOC":         {},
	"aXXo":        {},
	"BiTOR":       {},
	"BMDRu":       {},
	"BRrip":       {},
	"C4K":         {},
	"CM8":         {},
	"CREATiVE24":  {},
	"CrEwSaDe":    {},
	"CRUCiBLE":    {},
	"CTFOH":       {},
	"d3g":         {},
	"DNL":         {},
	"EASports":    {},
	"EVO":         {},
	"FaNGDiNG0":   {},
	"FGT":         {},
	"Flights":     {},
	"HD2DVD":      {},
	"HDT":         {},
	"HDTime":      {},
	"ION10":       {},
	"iPlanet":     {},
	"iVy":         {},
	"KiNGDOM":     {},
	"LAMA":        {},
	"MeGusta":     {},
	"MezRips":     {},
	"mHD":         {},
	"mSD":         {},
	"NhaNc3":      {},
	"nHD":         {},
	"nikt0":       {},
	"nSD":         {},
	"OFT":         {},
	"PRODJi":      {},
	"ProRes":      {},
	"QxR":         {},
	"RARBG":       {},
	"ReaLHD":      {},
	"SANTi":       {},
	"SasukeducK":  {},
	"Sicario":     {},
	"SPiRiT":      {},
	"STUTTERSHIT": {},
	"SyncUP":      {},
	"Telly":       {},
	"TGS":         {},
	"tigole":      {},
	"TOMMY":       {},
	"ViSION":      {},
	"VXT":         {},
	"WAF":         {},
	"WKS":         {},
	"WORLD":       {},
	"x0r":         {},
}

func bannedGroups() []string {
	groups := slices.Collect(maps.Keys(antBannedReleaseGroups))
	slices.Sort(groups)
	return groups
}

func isBannedReleaseGroup(group string) bool {
	_, banned := antBannedReleaseGroups[group]
	return banned
}
