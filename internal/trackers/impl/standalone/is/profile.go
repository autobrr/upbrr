// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns IS identity, preparation, dupe, auth, and artifact behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                  "IS",
		BaseURL:               baseURL,
		DescriptionGroup:      "is",
		PrepareDescription:    prepareDescription,
		PrepareUpload:         prepareUpload,
		NewDuplicateAdapter:   newDuplicateAdapter,
		AuthCapability:        standalone.CookieAuthCapability("IS"),
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: sourceFlag},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{baseURL}},
	}
}

// New returns a fresh IS definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
