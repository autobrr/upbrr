// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"maps"
	"slices"
)

var antBannedReleaseGroups = map[string]struct{}{
	"3LTON":        {},
	"4yEo":         {},
	"ADE":          {},
	"AFG":          {},
	"AniHLS":       {},
	"AnimeRG":      {},
	"AniURL":       {},
	"AROMA":        {},
	"aXXo":         {},
	"Brrip":        {},
	"CHD":          {},
	"CM8":          {},
	"CrEwSaDe":     {},
	"d3g":          {},
	"DDR":          {},
	"DNL":          {},
	"DeadFish":     {},
	"ELiTE":        {},
	"eSc":          {},
	"EVO":          {},
	"FaNGDiNG0":    {},
	"FGT":          {},
	"FRDS":         {},
	"FUM":          {},
	"HAiKU":        {},
	"HD2DVD":       {},
	"HDS":          {},
	"HDTime":       {},
	"Hi10":         {},
	"ION10":        {},
	"iPlanet":      {},
	"JIVE":         {},
	"KiNGDOM":      {},
	"Leffe":        {},
	"LiGaS":        {},
	"LOAD":         {},
	"MeGusta":      {},
	"MkvCage":      {},
	"mHD":          {},
	"mSD":          {},
	"NhaNc3":       {},
	"nHD":          {},
	"NOIVTC":       {},
	"nSD":          {},
	"Oj":           {},
	"Ozlem":        {},
	"PiRaTeS":      {},
	"PRoDJi":       {},
	"RAPiDCOWS":    {},
	"RARBG":        {},
	"RetroPeeps":   {},
	"RDN":          {},
	"REsuRRecTioN": {},
	"RMTeam":       {},
	"SANTi":        {},
	"SicFoI":       {},
	"SPASM":        {},
	"SM737":        {},
	"SPDVD":        {},
	"STUTTERSHIT":  {},
	"TBS":          {},
	"Telly":        {},
	"TM":           {},
	"UPiNSMOKE":    {},
	"URANiME":      {},
	"WAF":          {},
	"xRed":         {},
	"XS":           {},
	"YIFY":         {},
	"YTS":          {},
	"Zeus":         {},
	"ZKBL":         {},
	"ZmN":          {},
	"ZMNT":         {},
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
