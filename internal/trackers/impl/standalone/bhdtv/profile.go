// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns BHDTV identity, preparation, dupe, and artifact behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                 "BHDTV",
		BaseURL:              "https://www.bit-hdtv.com",
		DescriptionGroup:     "bhdtv",
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		NewDuplicateAdapter:  func(dupe.Dependencies) dupe.Adapter { return bhdtvDuplicateAdapter{} },
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "BIT-HDTV", UseMyAnnounce: true},
	}
}

// New returns a fresh BHDTV definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
