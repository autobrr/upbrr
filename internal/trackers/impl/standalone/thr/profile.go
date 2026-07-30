// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns THR identity, preparation, dupe, auth, and policy behavior,
// including the strict requirement for matching TMDB or IMDb metadata.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "THR",
		BaseURL:            baseURL,
		DescriptionGroup:   "thr",
		UploadContentMode:  trackers.UploadContentModeDescription,
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.NewReleaseNamePolicy("standalone/thr/v1", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
			if input.RequestedName != nil {
				subject := input.Subject
				subject.ReleaseName = *input.RequestedName
				return trackers.ResolvedReleaseNames{Upload: resolveName(subject)}, nil
			}
			if override := strings.TrimSpace(standalone.QuestionnaireAnswers(input.Subject, "THR")["name_override"]); override != "" {
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
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: sourceFlag},
		ImageHostPolicy: &trackers.ImageHostPolicy{
			AllowedHosts:      []string{"thr"},
			OwnedHosts:        []string{"thr"},
			DisableWithoutAPI: true,
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"torrenthr"}},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:         "THR",
			DisplayName:       "THR",
			AuthKind:          "credential_login",
			SupportsLogin:     true,
			SupportsAutoLogin: true,
		},
		AuthResolver: resolveAuthSession,
	}
}

// New returns a fresh THR definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
