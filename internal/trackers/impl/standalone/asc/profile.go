// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns ASC identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                    "ASC",
		BaseURL:                 baseURL,
		DescriptionGroup:        "asc",
		UploadContentMode:       trackers.UploadContentModeDescription,
		LocalizedMetadataLocale: "pt-BR",
		PrepareDescription:      prepareDescription,
		PrepareUpload:           prepareUpload,
		NewDuplicateAdapter:     newDuplicateAdapter,
		UploadArtifactPolicy:    &trackers.UploadArtifactPolicy{Source: sourceFlag},
		AudioPolicy:             &trackers.AudioPolicy{AllowBloat: true},
		TorrentIdentityPolicy:   &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"amigos-share.club"}},
		AuthCapability:          standalone.CookieAuthCapability("ASC"),
	}
}

// New returns a fresh ASC definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
