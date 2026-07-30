// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// CatalogDescriptors returns deterministic safe descriptors for all registered trackers.
func (r *Registry) CatalogDescriptors() ([]api.TrackerCatalogDescriptor, error) {
	if r == nil {
		return nil, nil
	}
	descriptors := make([]api.TrackerCatalogDescriptor, 0, len(r.descriptors))
	for _, name := range r.Names() {
		descriptor := r.descriptors[name]
		fingerprint, err := descriptorPolicyFingerprint(descriptor)
		if err != nil {
			return nil, fmt.Errorf("trackers: catalog descriptor %s: %w", name, err)
		}
		descriptors = append(descriptors, api.TrackerCatalogDescriptor{
			TrackerID:         api.TrackerID(descriptor.Name),
			DisplayName:       descriptor.DisplayName,
			Aliases:           slices.Clone(descriptor.Aliases),
			Family:            string(descriptor.Family),
			BaseURL:           descriptor.BaseURL,
			UploadContentMode: string(descriptor.UploadContentMode),
			DescriptionGroup:  descriptor.DescriptionGroup,
			MetadataLocale:    descriptor.MetadataLocale,
			ProjectorVersion:  descriptor.ProjectorVersion,
			PolicyFingerprint: fingerprint,
			Capabilities:      catalogCapabilities(descriptor),
		})
	}
	return descriptors, nil
}

func catalogCapabilities(descriptor Descriptor) api.TrackerCapabilityDescriptor {
	return api.TrackerCapabilityDescriptor{
		DuplicateSearch:        descriptor.Definition != nil,
		Rules:                  descriptor.Rules != nil || descriptor.Validation.Check != nil,
		Metadata:               descriptor.Metadata != nil,
		LocalizedMetadata:      strings.TrimSpace(descriptor.MetadataLocale) != "",
		TrackerData:            descriptor.DataFactory != nil,
		Claims:                 descriptor.ClaimFactory != nil || descriptor.ClaimPolicy != nil,
		Authentication:         descriptor.AuthResolver != nil || descriptor.AuthCapability != nil,
		StaticBannedGroups:     len(descriptor.BannedGroups) > 0,
		DynamicBannedGroups:    descriptor.BannedPolicy != nil,
		ScreenshotRequirement:  descriptor.UploadContentMode.UsesImages(),
		Description:            descriptor.UploadContentMode.UsesDescription(),
		ImageHosting:           descriptor.ImageHost != nil,
		TorrentPersonalization: descriptor.UploadArtifact != nil,
	}
}

func descriptorPolicyFingerprint(descriptor Descriptor) (api.WorkflowFingerprint, error) {
	rules := safeRuleFingerprint(descriptor.Rules)
	authPolicy := safeAuthPolicyFingerprint(descriptor.AuthPolicy)
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Name              string
		Family            Family
		ProjectorVersion  string
		ReleaseNamePolicy string
		UploadContentMode UploadContentMode
		Rules             *ruleFingerprint
		ValidationPolicy  string
		Artifact          *ArtifactPolicy
		DataPolicy        *DataLookupPolicy
		BannedGroups      []string
		BannedPolicy      *BannedGroupPolicy
		Metadata          *TrackerMetadataPolicy
		UploadArtifact    *UploadArtifactPolicy
		DupePolicy        *DupePolicy
		AudioPolicy       *AudioPolicy
		ImageHost         *ImageHostPolicy
		TorrentIdentity   *TorrentIdentityPolicy
		ClaimPolicy       *ClaimPolicy
		AuthCapability    *api.TrackerAuthCapability
		AuthPolicy        *authPolicyFingerprint
		HasAPIKeyResolver bool
		MetadataLocale    string
		DescriptionGroup  string
	}{
		Name:              descriptor.Name,
		Family:            descriptor.Family,
		ProjectorVersion:  descriptor.ProjectorVersion,
		ReleaseNamePolicy: descriptor.ReleaseNamePolicy.ID,
		UploadContentMode: descriptor.UploadContentMode,
		Rules:             rules,
		ValidationPolicy:  descriptor.Validation.ID,
		Artifact:          descriptor.Artifact,
		DataPolicy:        descriptor.DataPolicy,
		BannedGroups:      slices.Clone(descriptor.BannedGroups),
		BannedPolicy:      descriptor.BannedPolicy,
		Metadata:          descriptor.Metadata,
		UploadArtifact:    descriptor.UploadArtifact,
		DupePolicy:        descriptor.DupePolicy,
		AudioPolicy:       descriptor.AudioPolicy,
		ImageHost:         descriptor.ImageHost,
		TorrentIdentity:   descriptor.TorrentIdentity,
		ClaimPolicy:       descriptor.ClaimPolicy,
		AuthCapability:    descriptor.AuthCapability,
		AuthPolicy:        authPolicy,
		HasAPIKeyResolver: descriptor.AuthPolicy != nil && descriptor.AuthPolicy.ResolveAPIKey != nil,
		MetadataLocale:    descriptor.MetadataLocale,
		DescriptionGroup:  descriptor.DescriptionGroup,
	})
	if err != nil {
		return "", fmt.Errorf("tracker policy fingerprint: %w", err)
	}
	return fingerprint, nil
}

type ruleFingerprint struct {
	RequireUniqueID          bool
	RequireValidMISetting    bool
	RequireAudioLanguages    bool
	RequireDiscOnly          bool
	RequireMovieOnly         bool
	RequireMovieUnlessTVPack bool
	RequireTVOnly            bool
	RequireHEVCForTypes      []string
	MinResolution            string
	BlockAdult               bool
	AdultMessage             string
	Language                 *LanguageRule
	BlockDVDRip              bool
	BlockExternalSubs        bool
	BlockSingleFileFolder    bool
	BlockHardcodedSubs       bool
	BlockGroups              []string
	BlockGroupUnlessType     map[string][]string
	RequireSceneNFO          bool
}

func safeRuleFingerprint(rules *RuleSet) *ruleFingerprint {
	if rules == nil {
		return nil
	}
	return &ruleFingerprint{
		RequireUniqueID:          rules.RequireUniqueID,
		RequireValidMISetting:    rules.RequireValidMISetting,
		RequireAudioLanguages:    rules.RequireAudioLanguages,
		RequireDiscOnly:          rules.RequireDiscOnly,
		RequireMovieOnly:         rules.RequireMovieOnly,
		RequireMovieUnlessTVPack: rules.RequireMovieUnlessTVPack,
		RequireTVOnly:            rules.RequireTVOnly,
		RequireHEVCForTypes:      slices.Clone(rules.RequireHEVCForTypes),
		MinResolution:            rules.MinResolution,
		BlockAdult:               rules.BlockAdult,
		AdultMessage:             rules.AdultMessage,
		Language:                 rules.Language,
		BlockDVDRip:              rules.BlockDVDRip,
		BlockExternalSubs:        rules.BlockExternalSubs,
		BlockSingleFileFolder:    rules.BlockSingleFileFolder,
		BlockHardcodedSubs:       rules.BlockHardcodedSubs,
		BlockGroups:              slices.Clone(rules.BlockGroups),
		BlockGroupUnlessType:     rules.BlockGroupUnlessType,
		RequireSceneNFO:          rules.RequireSceneNFO,
	}
}

type authPolicyFingerprint struct {
	HasRequirementsResolver     bool
	APIKeyRequiresUploadSession bool
	CookieCompletesAPIKeyAuth   bool
	MissingAPIKeyMessage        string
	UploadSessionMissingMessage string
	LoginRequiresAnnounceURL    bool
	PasskeyCoversAuth           bool
	PasskeyRequiresUsername     bool
	PasskeyRequiresCookie       bool
}

func safeAuthPolicyFingerprint(policy *AuthPolicy) *authPolicyFingerprint {
	if policy == nil {
		return nil
	}
	return &authPolicyFingerprint{
		HasRequirementsResolver:     policy.ResolveRequirements != nil,
		APIKeyRequiresUploadSession: policy.APIKeyRequiresUploadSession,
		CookieCompletesAPIKeyAuth:   policy.CookieCompletesAPIKeyAuth,
		MissingAPIKeyMessage:        policy.MissingAPIKeyMessage,
		UploadSessionMissingMessage: policy.UploadSessionMissingMessage,
		LoginRequiresAnnounceURL:    policy.LoginRequiresAnnounceURL,
		PasskeyCoversAuth:           policy.PasskeyCoversAuth,
		PasskeyRequiresUsername:     policy.PasskeyRequiresUsername,
		PasskeyRequiresCookie:       policy.PasskeyRequiresCookie,
	}
}
