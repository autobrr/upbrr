// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns HDS identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "HDS",
		BaseURL:             baseURL,
		DescriptionGroup:    "hds",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
		Rules:               rules(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{
			Source: sourceFlag,
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"hd-space.pw"},
		},
		AuthCapability: &api.TrackerAuthCapability{
			AuthKind:           "cookies",
			SupportsCookieFile: true,
		},
	}
}

// New returns a fresh HDS definition from its tracker-local profile.
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
	return trackers.DescriptionResult{Group: "hds", Description: description}, nil
}
