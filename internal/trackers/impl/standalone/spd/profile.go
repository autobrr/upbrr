// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns SPD identity, preparation, dupe, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "SPD",
		BaseURL:             baseURL,
		DescriptionGroup:    "spd",
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
		BannedGroupPolicy: &trackers.BannedGroupPolicy{
			DefaultEndpoint:   baseURL + "/api/torrent/release-group/blacklist",
			EndpointPath:      "/api/torrent/release-group/blacklist",
			RequireAPIKey:     true,
			RawAPIKeyFallback: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
			}},
		},
		AudioPolicy: &trackers.AudioPolicy{
			AllowedLanguages: []string{"romanian"},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"ramjet.speedapp.io", "ramjet.speedapp.to", "ramjet.speedappio.org"},
		},
	}
}

// New returns a fresh SPD definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)
	return trackers.DescriptionResult{Group: "spd", Description: description}, nil
}
