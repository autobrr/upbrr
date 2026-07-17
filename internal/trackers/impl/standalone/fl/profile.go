// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/internal/cookieauth"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns FL identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "FL",
		BaseURL:             baseURL,
		DescriptionGroup:    "fl",
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
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
		AuthResolver: cookieauth.CookieLoginResolver(cookieauth.CookieLoginSpec{
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

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	return trackers.DescriptionResult{Group: "fl", Description: buildDescription(assets)}, nil
}
