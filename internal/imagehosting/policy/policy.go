// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package policy resolves tracker image-host restrictions and upload capabilities.
package policy

import (
	"maps"
	"slices"
	"strings"
)

// Policy describes the image hosts accepted and preferred by one tracker.
type Policy struct {
	// AllowedHosts lists normalized hosts accepted in a tracker description.
	AllowedHosts []string
	// UploadHosts is the allowed subset for which upbrr has an uploader.
	UploadHosts []string
	// PreferredHosts orders upload-capable hosts for automatic selection.
	PreferredHosts []string
	// Required reports that descriptions must use an allowed host.
	Required bool
}

// Metadata is a defensive snapshot of known image-host policy data.
type Metadata struct {
	// UploadHosts lists every configured uploader name.
	UploadHosts []string
	// TrackerUploadHosts maps tracker names to upload-capable accepted hosts.
	TrackerUploadHosts map[string][]string
	// OwnedHosts maps normalized host names to their owning tracker.
	OwnedHosts map[string]string
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

// ForTracker returns the active image-host policy for tracker and its runtime gates.
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

// KnownTrackerPolicies returns accepted hosts keyed by tracker name.
func KnownTrackerPolicies() map[string][]string {
	out := make(map[string][]string, len(trackerAllowedHosts))
	for tracker, hosts := range trackerAllowedHosts {
		out[tracker] = append([]string(nil), hosts...)
	}
	return out
}

// Snapshot returns copies of all image-host policy metadata.
func Snapshot() Metadata {
	return Metadata{
		UploadHosts:        KnownUploadHosts(),
		TrackerUploadHosts: KnownTrackerUploadPolicies(),
		OwnedHosts:         KnownOwnedHosts(),
	}
}

// KnownUploadHosts returns supported upload host names in deterministic order.
func KnownUploadHosts() []string {
	out := make([]string, 0, len(uploadHosts))
	for host := range uploadHosts {
		out = append(out, host)
	}
	slices.Sort(out)
	return out
}

// KnownTrackerUploadPolicies returns upload-capable hosts keyed by tracker name.
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

// KnownOwnedHosts returns tracker ownership keyed by normalized host name.
func KnownOwnedHosts() map[string]string {
	out := make(map[string]string, len(ownedHosts))
	maps.Copy(out, ownedHosts)
	return out
}

// IsUploadHost reports whether host has a configured uploader.
func IsUploadHost(host string) bool {
	_, ok := uploadHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

// OwnerForHost returns the tracker that owns host, or an empty string when unowned.
func OwnerForHost(host string) string {
	return ownedHosts[strings.ToLower(strings.TrimSpace(host))]
}

// HostAllowed reports whether host is present in allowed; an empty allowlist permits every host.
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
