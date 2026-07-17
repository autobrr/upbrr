// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns PTS identity, preparation, dupe, auth, and artifact behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                  "PTS",
		BaseURL:               baseURL,
		DescriptionGroup:      "pts",
		UploadContentMode:     trackers.UploadContentModeDescription,
		PrepareDescription:    prepareDescription,
		PrepareUpload:         prepareUpload,
		NewDuplicateAdapter:   newDuplicateAdapter,
		AuthCapability:        standalone.CookieAuthCapability("PTS"),
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: sourceFlag},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"https://tracker.ptskit.com"}},
	}
}

// New returns a fresh PTS definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
