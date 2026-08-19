// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns BT identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                    "BT",
		BaseURL:                 baseURL,
		DescriptionGroup:        "bt",
		UploadContentMode:       trackers.UploadContentModeDescription,
		LocalizedMetadataLocale: "pt-BR",
		PrepareDescription:      prepareDescription,
		PrepareUpload:           prepareUpload,
		ValidationPolicy:        validationPolicy(),
		ReleaseNamePolicy:       trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/bt/v1", resolveUploadName, resolveSearchName),
		NewDuplicateAdapter:     newDuplicateAdapter,
		UploadArtifactPolicy:    &trackers.UploadArtifactPolicy{Source: sourceFlag, RequireAnnounce: true},
		AudioPolicy:             &trackers.AudioPolicy{AllowBloat: true},
		TorrentIdentityPolicy:   &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"t.brasiltracker.org"}},
		AuthCapability:          authcontract.CookieCapability("BT"),
	}
}

// New returns a fresh BT definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
