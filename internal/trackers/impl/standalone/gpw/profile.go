// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns GPW identity, preparation, dupe, bans, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                 "GPW",
		BaseURL:              baseURL,
		DescriptionGroup:     "gpw",
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		NewDuplicateAdapter:  newDuplicateAdapter,
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: sourceFlag},
		ImageHostPolicy: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"kshare", "pixhost", "pterclub", "ilikeshots", "imgbox"},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://tracker.greatposterwall.com"},
		},
	}
}

// New returns a fresh GPW definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
