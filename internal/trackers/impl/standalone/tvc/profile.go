// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns TVC identity, preparation, dupe, and policy behavior,
// including strict metadata, UHD, BDMV-disc, and remux restrictions.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "TVC",
		BaseURL:            "https://tvchaosuk.com",
		DescriptionGroup:   "tvc",
		UploadContentMode:  trackers.UploadContentModeDescription,
		AuthCapability:     authcontract.APIKeyCapability("TVC"),
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.NewReleaseNamePolicy("standalone/tvc/v1", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
			if input.RequestedName != nil {
				return trackers.ResolvedReleaseNames{Upload: strings.TrimSpace(*input.RequestedName)}, nil
			}
			if override := strings.TrimSpace(standalone.QuestionnaireAnswers(input.Subject, "TVC")["name_override"]); override != "" {
				return trackers.ResolvedReleaseNames{Upload: override}, nil
			}
			return trackers.ResolvedReleaseNames{Upload: resolveName(input.Subject)}, nil
		}),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: "TVCHAOS"},
		ImageHostPolicy:       &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbb", "imgbox", "pixhost", "bam", "onlyimage"}},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"https://tvchaosuk.com"}},
	}
}

// New returns a fresh TVC definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
