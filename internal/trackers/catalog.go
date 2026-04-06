// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"sort"

	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
)

var knownTrackers = map[string]struct{}{
	"ACM":    {},
	"ANT":    {},
	"AR":     {},
	"ASC":    {},
	"AZ":     {},
	"BHD":    {},
	"BHDTV":  {},
	"BJS":    {},
	"BT":     {},
	"BTN":    {},
	"CZ":     {},
	"DC":     {},
	"FF":     {},
	"FL":     {},
	"GPW":    {},
	"HDB":    {},
	"HDS":    {},
	"HDT":    {},
	"IS":     {},
	"MTV":    {},
	"NBL":    {},
	"PHD":    {},
	"PTP":    {},
	"PTS":    {},
	"RTF":    {},
	"SPD":    {},
	"THR":    {},
	"TL":     {},
	"TVC":    {},
	"MANUAL": {},
}

func init() {
	for _, tracker := range unit3dmeta.Trackers() {
		knownTrackers[tracker] = struct{}{}
	}
}

// KnownTrackers returns the sorted list of tracker names in the registry.
func KnownTrackers() []string {
	trackers := make([]string, 0, len(knownTrackers))
	for name := range knownTrackers {
		trackers = append(trackers, name)
	}
	sort.Strings(trackers)
	return trackers
}
