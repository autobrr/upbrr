// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns DC identity, preparation, dupe, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "DC",
		BaseURL:             baseURL,
		DescriptionGroup:    "dc",
		UploadContentMode:   trackers.UploadContentModeDescription,
		AuthCapability:      authcontract.APIKeyCapability("DC"),
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ValidationPolicy:    validationPolicy(),
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNamePolicy("standalone/dc/v1", resolveUploadName),
		NewDuplicateAdapter: newDuplicateAdapter,
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: sourceFlag,
		},
		AudioPolicy: &trackers.AudioPolicy{
			AllowBloat: true,
		},
		ImageHostPolicy: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"imgbox", "imgbb", "bhd", "imgur", "postimg", "sharex"},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.digitalcore.club", "trackerprxy.digitalcore.club"},
		},
	}
}

// New returns a fresh DC definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
