// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns BJS identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                    "BJS",
		BaseURL:                 baseURL,
		DescriptionGroup:        "bjs",
		LocalizedMetadataLocale: "pt-BR",
		PrepareDescription:      prepareDescription,
		PrepareUpload:           prepareUpload,
		NewDuplicateAdapter:     newDuplicateAdapter,
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB},
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: sourceFlag},
		AudioPolicy:           &trackers.AudioPolicy{AllowBloat: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"tracker.bj-share.info"}},
		AuthCapability:        standalone.CookieAuthCapability("BJS"),
	}
}

// New returns a fresh BJS definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
