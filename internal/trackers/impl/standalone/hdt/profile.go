// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns HDT identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "HDT",
		BaseURL:             resolveBaseURL(),
		DescriptionGroup:    "hdt",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/hdt/v1", resolveName, resolveSearchName),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source:          "hd-torrents.org",
			RequireAnnounce: true,
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://hdts-announce.ru"},
		},
		AuthCapability: &api.TrackerAuthCapability{
			AuthKind:           "cookies",
			SupportsCookieFile: true,
		},
	}
}

// New returns a fresh HDT definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
