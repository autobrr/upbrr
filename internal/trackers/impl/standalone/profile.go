// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// DescriptionPreparer builds tracker-local description content for one intent.
type DescriptionPreparer func(context.Context, trackers.PreparationInput) (trackers.DescriptionResult, error)

// Profile is the construction input for one standalone tracker's identity,
// preparation callbacks, duplicate adapter, and declarative capabilities.
// [New] normalizes identity fields and copies mutable policy data before the
// definition is published.
type Profile struct {
	Name                    string
	BaseURL                 string
	DescriptionGroup        string
	LocalizedMetadataLocale string
	UploadContentMode       trackers.UploadContentMode
	PrepareDescription      DescriptionPreparer
	PrepareUpload           trackers.UploadPreparer
	ReleaseNamePolicy       trackers.ReleaseNamePolicyBinding
	NewDuplicateAdapter     func(dupe.Dependencies) dupe.Adapter
	Rules                   *trackers.RuleSet
	ValidationPolicy        trackers.ValidationPolicyBinding
	ClaimPolicy             *trackers.ClaimPolicy
	DataPolicy              *trackers.DataLookupPolicy
	ArtifactPolicy          *trackers.ArtifactPolicy
	BannedGroups            []string
	BannedGroupPolicy       *trackers.BannedGroupPolicy
	MetadataPolicy          *trackers.TrackerMetadataPolicy
	UploadArtifactPolicy    *trackers.UploadArtifactPolicy
	DupePolicy              *trackers.DupePolicy
	AudioPolicy             *trackers.AudioPolicy
	ImageHostPolicy         *trackers.ImageHostPolicy
	TorrentIdentityPolicy   *trackers.TorrentIdentityPolicy
	AuthCapability          *api.TrackerAuthCapability
	AuthResolver            trackers.AuthSessionResolver
	AuthPolicy              *trackers.AuthPolicy
	AuthStateManager        trackers.AuthStateManager
}
