// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
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
		ValidationPolicy:        validationPolicy(),
		ReleaseNamePolicy: trackers.NewReleaseNamePolicy("standalone/asc/v1", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
			uploadName := resolveUploadTitle(input.Subject)
			if input.RequestedName != nil {
				uploadName = strings.TrimSpace(*input.RequestedName)
			}
			searchName := uploadName
			if input.Subject.Anime {
				searchName = resolveSearchTitle(input.Subject)
			}
			return trackers.ResolvedReleaseNames{Upload: uploadName, Duplicate: searchName}, nil
		}),
		NewDuplicateAdapter:   newDuplicateAdapter,
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: sourceFlag, RequireAnnounce: true},
		AudioPolicy:           &trackers.AudioPolicy{AllowBloat: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"amigos-share.club"}},
		AuthCapability:        authcontract.CookieCapability("ASC"),
	}
}

// New returns a fresh ASC definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
