// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition implements shared standalone identity, preparation, duplicate,
// and declarative capability contracts from one immutable profile.
type Definition struct {
	profile Profile
}

// New validates, normalizes, and defensively copies a standalone profile.
func New(profile Profile) (*Definition, error) {
	profile.Name = strings.ToUpper(strings.TrimSpace(profile.Name))
	profile.BaseURL = strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
	profile.DescriptionGroup = strings.ToLower(strings.TrimSpace(profile.DescriptionGroup))
	profile.LocalizedMetadataLocale = strings.TrimSpace(profile.LocalizedMetadataLocale)
	if profile.DescriptionGroup == "" {
		profile.DescriptionGroup = strings.ToLower(profile.Name)
	}
	switch {
	case profile.Name == "":
		return nil, errors.New("standalone tracker profile has empty name")
	case profile.BaseURL == "":
		return nil, fmt.Errorf("standalone tracker profile %s has empty base URL", profile.Name)
	case !profile.UploadContentMode.Valid():
		return nil, fmt.Errorf("standalone tracker profile %s has invalid upload content mode %q", profile.Name, profile.UploadContentMode)
	case profile.UploadContentMode.UsesDescription() && profile.PrepareDescription == nil:
		return nil, fmt.Errorf("standalone tracker profile %s has no description preparer", profile.Name)
	case profile.PrepareUpload == nil:
		return nil, fmt.Errorf("standalone tracker profile %s has no upload preparer", profile.Name)
	case profile.NewDuplicateAdapter == nil:
		return nil, fmt.Errorf("standalone tracker profile %s has no duplicate adapter", profile.Name)
	}
	profile = cloneProfile(profile)
	if profile.AuthCapability != nil {
		profile.AuthCapability.TrackerID = profile.Name
		if strings.TrimSpace(profile.AuthCapability.DisplayName) == "" {
			profile.AuthCapability.DisplayName = profile.Name
		}
	}
	return &Definition{profile: profile}, nil
}

// MustNew returns a validated standalone definition or panics for an invalid
// compiled profile.
func MustNew(profile Profile) *Definition {
	definition, err := New(profile)
	if err != nil {
		panic(err)
	}
	return definition
}

// Name returns the normalized tracker identifier.
func (d *Definition) Name() string { return d.profile.Name }

// TrackerFamily identifies the definition as standalone.
func (*Definition) TrackerFamily() trackers.Family { return trackers.FamilyStandalone }

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (d *Definition) DefaultBaseURL() string { return d.profile.BaseURL }

// DescriptionGroup returns the profile-owned description override group.
func (d *Definition) DescriptionGroup() string { return d.profile.DescriptionGroup }

// LocalizedMetadataLocale returns the optional tracker-owned metadata locale.
func (d *Definition) LocalizedMetadataLocale() string { return d.profile.LocalizedMetadataLocale }

// UploadContentMode returns the profile-owned shared content workflow.
func (d *Definition) UploadContentMode() trackers.UploadContentMode {
	return d.profile.UploadContentMode
}

// Prepare dispatches intent through the tracker-local profile callbacks.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	if input.Intent == trackers.PreparationIntentDescriptionPreview && !d.profile.UploadContentMode.UsesDescription() {
		return trackers.TrackerPlan{}, trackers.NewPreparationFailure(
			input.Tracker,
			"capability",
			"tracker does not support shared description preparation",
			nil,
		)
	}
	return trackers.PrepareAdapter(ctx, input, d.profile.PrepareDescription, d.profile.PrepareUpload)
}

// NewDuplicateAdapter builds the tracker-local duplicate-search adapter.
func (d *Definition) NewDuplicateAdapter(dependencies dupe.Dependencies) dupe.Adapter {
	return d.profile.NewDuplicateAdapter(dependencies)
}

// Rules returns a defensive copy of tracker-owned validation rules.
func (d *Definition) Rules() *trackers.RuleSet { return cloneRules(d.profile.Rules) }

// ClaimPolicy returns tracker-owned claim orchestration policy.
func (d *Definition) ClaimPolicy() *trackers.ClaimPolicy { return cloneValue(d.profile.ClaimPolicy) }

// DataLookupPolicy returns tracker-owned lookup orchestration policy.
func (d *Definition) DataLookupPolicy() *trackers.DataLookupPolicy {
	return cloneValue(d.profile.DataPolicy)
}

// ArtifactPolicy returns tracker-owned torrent artifact limits.
func (d *Definition) ArtifactPolicy() *trackers.ArtifactPolicy {
	return cloneValue(d.profile.ArtifactPolicy)
}

// BannedGroups returns a defensive copy of static tracker bans.
func (d *Definition) BannedGroups() []string { return slices.Clone(d.profile.BannedGroups) }

// BannedGroupPolicy returns tracker-owned dynamic ban retrieval settings.
func (d *Definition) BannedGroupPolicy() *trackers.BannedGroupPolicy {
	return cloneValue(d.profile.BannedGroupPolicy)
}

// MetadataPolicy returns a defensive copy of tracker metadata requirements.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return cloneMetadataPolicy(d.profile.MetadataPolicy)
}

// UploadArtifactPolicy returns tracker torrent personalization settings.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return cloneValue(d.profile.UploadArtifactPolicy)
}

// DupePolicy returns tracker-specific duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy { return cloneValue(d.profile.DupePolicy) }

// AudioPolicy returns a defensive copy of tracker audio constraints.
func (d *Definition) AudioPolicy() *trackers.AudioPolicy {
	return cloneAudioPolicy(d.profile.AudioPolicy)
}

// ImageHostPolicy returns a defensive copy of tracker image-host constraints.
func (d *Definition) ImageHostPolicy() *trackers.ImageHostPolicy {
	return cloneImageHostPolicy(d.profile.ImageHostPolicy)
}

// TorrentIdentityPolicy returns a defensive copy of tracker identity patterns.
func (d *Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return cloneTorrentIdentityPolicy(d.profile.TorrentIdentityPolicy)
}

// AuthCapabilityDescriptor returns optional profile-owned auth metadata.
func (d *Definition) AuthCapabilityDescriptor() *api.TrackerAuthCapability {
	return cloneAuthCapability(d.profile.AuthCapability)
}

// AuthSessionResolver returns optional tracker-owned auth resolution behavior.
func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return d.profile.AuthResolver
}

// AuthPolicy returns optional tracker-owned auth coordinator settings.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy { return cloneValue(d.profile.AuthPolicy) }

// AuthStateManager returns optional tracker-owned persisted auth management.
func (d *Definition) AuthStateManager() trackers.AuthStateManager { return d.profile.AuthStateManager }

func cloneProfile(profile Profile) Profile {
	profile.Rules = cloneRules(profile.Rules)
	profile.ClaimPolicy = cloneValue(profile.ClaimPolicy)
	profile.DataPolicy = cloneValue(profile.DataPolicy)
	profile.ArtifactPolicy = cloneValue(profile.ArtifactPolicy)
	profile.BannedGroups = slices.Clone(profile.BannedGroups)
	profile.BannedGroupPolicy = cloneValue(profile.BannedGroupPolicy)
	profile.MetadataPolicy = cloneMetadataPolicy(profile.MetadataPolicy)
	profile.UploadArtifactPolicy = cloneValue(profile.UploadArtifactPolicy)
	profile.DupePolicy = cloneValue(profile.DupePolicy)
	profile.AudioPolicy = cloneAudioPolicy(profile.AudioPolicy)
	profile.ImageHostPolicy = cloneImageHostPolicy(profile.ImageHostPolicy)
	profile.TorrentIdentityPolicy = cloneTorrentIdentityPolicy(profile.TorrentIdentityPolicy)
	profile.AuthCapability = cloneAuthCapability(profile.AuthCapability)
	profile.AuthPolicy = cloneValue(profile.AuthPolicy)
	return profile
}

func cloneRules(rules *trackers.RuleSet) *trackers.RuleSet {
	if rules == nil {
		return nil
	}
	clone := *rules
	clone.RequireHEVCForTypes = slices.Clone(rules.RequireHEVCForTypes)
	clone.BlockGroups = slices.Clone(rules.BlockGroups)
	clone.BlockGroupUnlessType = make(map[string][]string, len(rules.BlockGroupUnlessType))
	for group, releaseTypes := range rules.BlockGroupUnlessType {
		clone.BlockGroupUnlessType[group] = slices.Clone(releaseTypes)
	}
	if rules.Language != nil {
		language := *rules.Language
		language.Languages = slices.Clone(rules.Language.Languages)
		clone.Language = &language
	}
	return &clone
}

func cloneMetadataPolicy(policy *trackers.TrackerMetadataPolicy) *trackers.TrackerMetadataPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	clone.Requirements = slices.Clone(policy.Requirements)
	for idx := range clone.Requirements {
		clone.Requirements[idx].AnyOf = slices.Clone(clone.Requirements[idx].AnyOf)
	}
	return &clone
}

func cloneAudioPolicy(policy *trackers.AudioPolicy) *trackers.AudioPolicy {
	clone := cloneValue(policy)
	if clone != nil {
		clone.AllowedLanguages = slices.Clone(policy.AllowedLanguages)
	}
	return clone
}

func cloneImageHostPolicy(policy *trackers.ImageHostPolicy) *trackers.ImageHostPolicy {
	clone := cloneValue(policy)
	if clone != nil {
		clone.AllowedHosts = slices.Clone(policy.AllowedHosts)
		clone.OwnedHosts = slices.Clone(policy.OwnedHosts)
	}
	return clone
}

func cloneTorrentIdentityPolicy(policy *trackers.TorrentIdentityPolicy) *trackers.TorrentIdentityPolicy {
	clone := cloneValue(policy)
	if clone != nil {
		clone.TrackerURLPatterns = slices.Clone(policy.TrackerURLPatterns)
		clone.CommentURLPatterns = slices.Clone(policy.CommentURLPatterns)
	}
	return clone
}

func cloneAuthCapability(capability *api.TrackerAuthCapability) *api.TrackerAuthCapability {
	clone := cloneValue(capability)
	if clone != nil {
		clone.Notes = slices.Clone(capability.Notes)
	}
	return clone
}

func cloneValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
