// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns FF identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "FF",
		BaseURL:             baseURL,
		DescriptionGroup:    "ff",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/ff/v1", resolveName, resolveSearchName),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: sourceFlag,
		},
		AudioPolicy: &trackers.AudioPolicy{
			AllowBloat: true,
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.funfile.org"},
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
			TrackerID:     "FF",
			BaseURL:       baseURL,
			CookieDomain:  "www.funfile.org",
			Validate:      validateAuthCookies,
			HasCredential: ffHasLoginCredentials,
			Login:         loginAuthSession,
		}),
	}
}

// New returns a fresh FF definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
