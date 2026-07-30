// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns FL identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "FL",
		BaseURL:            baseURL,
		DescriptionGroup:   "fl",
		UploadContentMode:  trackers.UploadContentModeDescription,
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.NewReleaseNamePolicy("standalone/fl/v1", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
			subject := input.Subject
			answers := standalone.QuestionnaireAnswers(subject, "FL")
			if input.RequestedName != nil {
				subject.ReleaseName = *input.RequestedName
				answers = nil
			}
			return trackers.ResolvedReleaseNames{
				Upload:    resolveName(subject, answers),
				Duplicate: resolveSearchName(input.Subject),
			}, nil
		}),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: "FL",
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"reactor.filelist", "reactor.thefl.org"},
		},
		AuthCapability: &api.TrackerAuthCapability{
			AuthKind:           "cookies_login",
			SupportsCookieFile: true,
			SupportsLogin:      true,
			SupportsAutoLogin:  true,
		},
		AuthPolicy: &trackers.AuthPolicy{
			ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
				"cookies_or_login",
				false,
				[]trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
				[]trackers.AuthRequirement{trackers.AuthRequirementCredentialLogin},
			)),
		},
		AuthResolver: trackerauth.CookieLoginResolver(trackerauth.CookieLoginSpec{
			TrackerID:     "FL",
			BaseURL:       baseURL,
			CookieDomain:  ".filelist.io",
			Validate:      validateAuthCookies,
			HasCredential: flHasLoginCredentials,
			Login:         loginAuthSession,
		}),
	}
}

// New returns a fresh FL definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
