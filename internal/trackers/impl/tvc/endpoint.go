// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import "github.com/autobrr/upbrr/internal/trackers"

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (definition) DefaultBaseURL() string { return "https://tvchaosuk.com" }

// TorrentIdentityPolicy returns tracker-owned torrent-client identity behavior.
func (definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"https://tvchaosuk.com"}}
}
