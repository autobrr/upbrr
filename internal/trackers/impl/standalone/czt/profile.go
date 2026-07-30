// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns CZT identity, preparation, dupe, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                trackerName,
		BaseURL:             defaultBaseURL,
		DescriptionGroup:    descGroup,
		UploadContentMode:   trackers.UploadContentModeDescription,
		AuthCapability:      authcontract.PasskeyCapability(trackerName),
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ValidationPolicy:    validationPolicy(),
		ReleaseNamePolicy:   trackers.SceneFirstReleaseNamePolicy(),
		NewDuplicateAdapter: newDuplicateAdapter,
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: "CzT"},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"czteam.me"}},
	}
}

// New returns a fresh CZT definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
