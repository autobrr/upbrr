// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import "github.com/autobrr/upbrr/internal/trackers"

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (Definition) DefaultBaseURL() string { return "https://bj-share.info" }

// TorrentIdentityPolicy returns tracker-owned torrent-client identity behavior.
func (Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"tracker.bj-share.info"}}
}
