// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3dmeta

import (
	"sort"
	"strings"
)

var trackerBaseURLs = map[string]string{
	"A4K":    "https://aura4k.net",
	"ACM":    "https://eiga.moi",
	"AITHER": "https://aither.cc",
	"BLU":    "https://blutopia.cc",
	"CBR":    "https://capybarabr.com",
	"DP":     "https://darkpeers.org",
	"EMUW":   "https://emuwarez.com",
	"FRIKI":  "https://frikibar.com",
	"HHD":    "https://homiehelpdesk.net",
	"IHD":    "https://infinityhd.net",
	"ITT":    "https://itatorrents.xyz",
	"LCD":    "https://locadora.cc",
	"LDU":    "https://theldu.to",
	"LST":    "https://lst.gg",
	"LT":     "https://lat-team.com",
	"LUME":   "https://luminarr.me",
	"OE":     "https://onlyencodes.cc",
	"OTW":    "https://oldtoons.world",
	"PT":     "https://portugas.org",
	"PTT":    "https://polishtorrent.top",
	"R4E":    "https://racing4everyone.eu",
	"RAS":    "https://rastastugan.org",
	"RF":     "https://reelflix.cc",
	"SAM":    "https://samaritano.cc",
	"SHRI":   "https://shareisland.org",
	"SP":     "https://seedpool.org",
	"STC":    "https://skipthecommercials.xyz",
	"TIK":    "https://cinematik.net",
	"TLZ":    "https://tlzdigital.com",
	"TOS":    "https://theoldschool.cc",
	"TTR":    "https://torrenteros.org",
	"ULCX":   "https://upload.cx",
	"UTP":    "https://utp.to",
	"YUS":    "https://yu-scene.net",
}

func DefaultTracker() string {
	return "AITHER"
}

func Trackers() []string {
	trackers := make([]string, 0, len(trackerBaseURLs))
	for tracker := range trackerBaseURLs {
		trackers = append(trackers, tracker)
	}
	sort.Strings(trackers)
	return trackers
}

func BaseURL(tracker string) (string, bool) {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return "", false
	}
	baseURL, ok := trackerBaseURLs[key]
	return baseURL, ok
}

func IsKnown(tracker string) bool {
	_, ok := BaseURL(tracker)
	return ok
}
