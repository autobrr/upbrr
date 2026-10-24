// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns LST's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:          "LST",
		BaseURL:       "https://lst.gg",
		Rules:         Rules(),
		WorkflowMedia: &trackers.WorkflowMediaRequirements{AllowImportedScreenshotReferences: true},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			InspectProgrammeTracks: true,
			Requirements: []trackers.MetadataRequirement{
				{
					Scope:       trackers.MetadataScopeAny,
					AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
					Disposition: api.RuleDispositionStrict,
				},
			},
		},
		ValidationPolicy:  trackers.WithLanguageAssessment(validationPolicy(), languageAssessment),
		TitleSearchPolicy: titleSearchPolicy(),
		Site: unit3d.SiteProfile{
			FinalizeDescription:      finalizeImportedScreenshotDescription,
			ProjectionQuestionnaire:  languageQuestionnaire,
			ApplyAdditionalPayload:   additionalPayload,
			SubmissionRequirements:   submissionRequirements,
			ValidateFinalDescription: validateFinalDescription,
			AdjustTitleSearchParams:  titleSearchParams,
		},
		DupePolicy: duplicatePolicy(),
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/bannedReleaseGroups",
			RequireAPIKey: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "lostimg",
			OwnedHosts:             []string{"lostimg"},
			EnableWithImageHosting: true,
		},
	}
}
