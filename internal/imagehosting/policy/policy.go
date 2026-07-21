// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package policy describes generic image uploader capabilities. Tracker-owned
// acceptance and ownership rules are exposed by the tracker registry.
package policy

import (
	"slices"
	"strings"
)

// Metadata is a defensive snapshot of uploader and composed tracker policy data.
type Metadata struct {
	// UploadHosts lists every configured uploader name.
	UploadHosts []string
	// TrackerUploadHosts maps tracker names to upload-capable accepted hosts.
	TrackerUploadHosts map[string][]string
	// OwnedHosts maps normalized host names to their owning tracker.
	OwnedHosts map[string]string
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

// KnownUploadHosts returns supported upload host names in deterministic order.
func KnownUploadHosts() []string {
	out := make([]string, 0, len(uploadHosts))
	for host := range uploadHosts {
		out = append(out, host)
	}
	slices.Sort(out)
	return out
}

// IsUploadHost reports whether host has a configured uploader.
func IsUploadHost(host string) bool {
	_, ok := uploadHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

// HostAllowed reports whether host is present in allowed; an empty allowlist permits every host.
func HostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	needle := strings.ToLower(strings.TrimSpace(host))
	return slices.ContainsFunc(allowed, func(item string) bool {
		return strings.ToLower(strings.TrimSpace(item)) == needle
	})
}
