// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import "github.com/autobrr/upbrr/internal/trackers"

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (d *Definition) DefaultBaseURL() string { return d.baseURL }

// TorrentIdentityPolicy returns tracker-owned torrent-client identity behavior.
func (d *Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return &trackers.TorrentIdentityPolicy{
		TrackerURLPatterns: []string{"https://tracker.hdbits.org"},
		CommentURLPatterns: []string{d.baseURL},
		DetailIDPattern:    "id=(\\d+)",
	}
}
