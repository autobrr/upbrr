// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehostpolicy

import (
	"maps"
	"slices"
	"strings"
)

type Policy struct {
	AllowedHosts   []string
	UploadHosts    []string
	PreferredHosts []string
	Required       bool
}

type Metadata struct {
	UploadHosts        []string
	TrackerUploadHosts map[string][]string
	OwnedHosts         map[string]string
}

var trackerAllowedHosts = map[string][]string{
	"A4K": {"onlyimage", "imgbox", "ptscreens", "imgbb", "imgur", "postimg"},
	"BHD": {"imgbox", "imgbb", "pixhost", "bhd", "bam"},
	"DC":  {"imgbox", "imgbb", "bhd", "imgur", "postimg", "sharex"},
	"GPW": {"kshare", "pixhost", "pterclub", "ilikeshots", "imgbox"},
	"HDB": {"hdb"},
	"MTV": {"imgbox", "imgbb"},
	"OE":  {"imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage"},
	"PTP": {"pixhost", "imgbb", "onlyimage", "ptscreens", "passtheimage"},
	"STC": {"imgbox", "imgbb"},
	"THR": {"thr"},
	"TVC": {"imgbb", "imgbox", "pixhost", "bam", "onlyimage"},
}

var uploadHosts = map[string]struct{}{
	"dalexni":      {},
	"hdb":          {},
	"imgbb":        {},
	"imgbox":       {},
	"lensdump":     {},
	"lostimg":      {},
	"onlyimage":    {},
	"passtheimage": {},
	"pixhost":      {},
	"ptscreens":    {},
	"reelflix":     {},
	"seedpool_cdn": {},
	"sharex":       {},
	"thr":          {},
	"utppm":        {},
	"zipline":      {},
}

var ownedHosts = map[string]string{
	"hdb":      "HDB",
	"lostimg":  "LST",
	"reelflix": "RF",
	"thr":      "THR",
}

var trackerOptionalUploadHosts = map[string][]string{
	"LST": {"lostimg"},
	"RF":  {"reelflix"},
}

func ForTracker(tracker string, imgRehost bool, imgAPI string) Policy {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "HDB" && !imgRehost {
		return Policy{}
	}
	if name == "THR" && strings.TrimSpace(imgAPI) == "" {
		return Policy{}
	}
	allowed, ok := trackerAllowedHosts[name]
	if !ok {
		return Policy{}
	}
	normalizedAllowed := normalizeUnique(allowed...)
	return Policy{
		AllowedHosts:   normalizedAllowed,
		UploadHosts:    filterUploadHosts(normalizedAllowed),
		PreferredHosts: filterUploadHosts(normalizedAllowed),
		Required:       len(normalizedAllowed) > 0,
	}
}

func KnownTrackerPolicies() map[string][]string {
	out := make(map[string][]string, len(trackerAllowedHosts))
	for tracker, hosts := range trackerAllowedHosts {
		out[tracker] = append([]string(nil), hosts...)
	}
	return out
}

func PolicyMetadata() Metadata {
	return Metadata{
		UploadHosts:        KnownUploadHosts(),
		TrackerUploadHosts: KnownTrackerUploadPolicies(),
		OwnedHosts:         KnownOwnedHosts(),
	}
}

func KnownUploadHosts() []string {
	out := make([]string, 0, len(uploadHosts))
	for host := range uploadHosts {
		out = append(out, host)
	}
	slices.Sort(out)
	return out
}

func KnownTrackerUploadPolicies() map[string][]string {
	out := make(map[string][]string, len(trackerAllowedHosts))
	for tracker, hosts := range trackerAllowedHosts {
		out[tracker] = filterUploadHosts(normalizeUnique(hosts...))
	}
	for tracker, hosts := range trackerOptionalUploadHosts {
		existing := out[tracker]
		out[tracker] = filterUploadHosts(normalizeUnique(append(existing, hosts...)...))
	}
	return out
}

func KnownOwnedHosts() map[string]string {
	out := make(map[string]string, len(ownedHosts))
	maps.Copy(out, ownedHosts)
	return out
}

func IsUploadHost(host string) bool {
	_, ok := uploadHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

func OwnerForHost(host string) string {
	return ownedHosts[strings.ToLower(strings.TrimSpace(host))]
}

func HostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	needle := strings.ToLower(strings.TrimSpace(host))
	for _, item := range allowed {
		if strings.ToLower(strings.TrimSpace(item)) == needle {
			return true
		}
	}
	return false
}

func normalizeUnique(hosts ...string) []string {
	out := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		trimmed := strings.ToLower(strings.TrimSpace(host))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func filterUploadHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if IsUploadHost(host) {
			out = append(out, strings.ToLower(strings.TrimSpace(host)))
		}
	}
	return out
}
