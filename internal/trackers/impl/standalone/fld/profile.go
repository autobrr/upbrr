// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns FLD identity, preparation, dupe, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "FLD",
		BaseURL:             baseURL,
		DescriptionGroup:    "fld",
		UploadContentMode:   trackers.UploadContentModeDescription,
		AuthCapability:      authcontract.APIKeyCapability("FLD"),
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ValidationPolicy:    validationPolicy(),
		ReleaseNamePolicy:   namePolicy(),
		NewDuplicateAdapter: newDuplicateAdapter,
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: sourceFlag,
		},
		BannedGroups: bannedGroups(),
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"flood.st"},
		},
	}
}

// New returns a fresh FLD definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
