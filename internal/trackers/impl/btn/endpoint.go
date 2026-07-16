// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import "github.com/autobrr/upbrr/internal/trackers"

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (d *Definition) DefaultBaseURL() string { return d.baseURL }

// TorrentIdentityPolicy returns tracker-owned torrent-client identity behavior.
func (*Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return &trackers.TorrentIdentityPolicy{
		TrackerURLPatterns:       []string{"https://broadcasthe.net", "https://backup.landof.tv", "https://landof.tv", "landof.tv/"},
		CommentURLPatterns:       []string{"https://broadcasthe.net", "https://backup.landof.tv", "https://landof.tv"},
		DetailIDPattern:          "id=(\\d+)",
		InferMatchFromResolvedID: true,
	}
}
