// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/internal/cookieauth"
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
		NewDuplicateAdapter: newDuplicateAdapter,
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
		AuthResolver: cookieauth.CookieLoginResolver(cookieauth.CookieLoginSpec{
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

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	return trackers.DescriptionResult{Group: "ff", Description: buildDescription(assets)}, nil
}
