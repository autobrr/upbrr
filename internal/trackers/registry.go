// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

// Registry stores tracker definitions and their optional typed capabilities.
// Tracker names are normalized for case-insensitive lookup.
type Registry struct {
	descriptors map[string]Descriptor
	priority    []string
}

// SetPriorityOrder configures the curated tracker preference order.
func (r *Registry) SetPriorityOrder(names []string) {
	if r == nil {
		return
	}
	r.priority = normalizeRegistryNames(names)
}

// Priority returns curated names followed by remaining Unit3D names.
func (r *Registry) Priority() []string {
	if r == nil {
		return nil
	}
	ordered := append([]string(nil), r.priority...)
	seen := make(map[string]struct{}, len(ordered))
	for _, name := range ordered {
		seen[name] = struct{}{}
	}
	for _, name := range r.NamesByKind(KindUnit3D) {
		lower := strings.ToLower(name)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		ordered = append(ordered, lower)
	}
	return ordered
}

func normalizeRegistryNames(names []string) []string {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" {
			continue
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		normalized = append(normalized, lower)
	}
	return normalized
}

// NewRegistry returns an empty tracker registry.
func NewRegistry() *Registry {
	return &Registry{descriptors: make(map[string]Descriptor)}
}

// Register discovers the optional capabilities implemented by def and registers
// the resulting descriptor. It rejects nil, unnamed, mismatched, or duplicate definitions.
func (r *Registry) Register(def Definition) error {
	descriptor := Descriptor{Definition: def}
	if def != nil {
		descriptor.Name = def.Name()
		descriptor.Kind = KindNonUnit3D
		if provider, ok := def.(BaseURLProvider); ok {
			descriptor.BaseURL = strings.TrimSpace(provider.DefaultBaseURL())
		}
		if provider, ok := def.(KindProvider); ok {
			descriptor.Kind = provider.TrackerKind()
		}
		if provider, ok := def.(LocalizedMetadataProvider); ok {
			descriptor.MetadataLocale = strings.TrimSpace(provider.LocalizedMetadataLocale())
		}
		if provider, ok := def.(DescriptionGroupProvider); ok {
			descriptor.DescriptionGroup = strings.ToLower(strings.TrimSpace(provider.DescriptionGroup()))
		}
		descriptor.DataFactory, _ = def.(DataLookupFactory)
		descriptor.ClaimFactory, _ = def.(ClaimCheckerFactory)
		if provider, ok := def.(ClaimPolicyProvider); ok {
			descriptor.ClaimPolicy = provider.ClaimPolicy()
		}
		if provider, ok := def.(RuleProvider); ok {
			descriptor.Rules = provider.Rules()
		}
		if provider, ok := def.(DataLookupPolicyProvider); ok {
			descriptor.DataPolicy = provider.DataLookupPolicy()
		}
		if provider, ok := def.(ArtifactPolicyProvider); ok {
			descriptor.Artifact = provider.ArtifactPolicy()
		}
		if provider, ok := def.(BannedGroupsProvider); ok {
			descriptor.BannedGroups = append([]string(nil), provider.BannedGroups()...)
		}
		if provider, ok := def.(BannedGroupPolicyProvider); ok {
			descriptor.BannedPolicy = provider.BannedGroupPolicy()
		}
		if provider, ok := def.(MetadataPolicyProvider); ok {
			descriptor.Metadata = provider.MetadataPolicy()
		}
		if provider, ok := def.(UploadArtifactPolicyProvider); ok {
			descriptor.UploadArtifact = provider.UploadArtifactPolicy()
		}
		if provider, ok := def.(DupePolicyProvider); ok {
			descriptor.DupePolicy = provider.DupePolicy()
		}
		if provider, ok := def.(AudioPolicyProvider); ok {
			descriptor.AudioPolicy = provider.AudioPolicy()
		}
		if provider, ok := def.(ImageHostPolicyProvider); ok {
			descriptor.ImageHost = provider.ImageHostPolicy()
		}
		if provider, ok := def.(AuthSessionProvider); ok {
			descriptor.AuthResolver = provider.AuthSessionResolver()
		}
		if provider, ok := def.(AuthCapabilityProvider); ok {
			capability := provider.AuthCapability()
			descriptor.AuthCapability = &capability
		}
	}
	return r.RegisterDescriptor(descriptor)
}

// NeedsLocalizedMetadata reports whether any registered tracker consumes locale.
func (r *Registry) NeedsLocalizedMetadata(names []string, locale string) bool {
	if r == nil {
		return false
	}
	for _, name := range names {
		descriptor, ok := r.LookupDescriptor(name)
		if ok && strings.EqualFold(strings.TrimSpace(descriptor.MetadataLocale), strings.TrimSpace(locale)) {
			return true
		}
	}
	return false
}

// LookupClaimPolicy returns tracker-owned generic claim orchestration policy.
func (r *Registry) LookupClaimPolicy(tracker string) (ClaimPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.ClaimPolicy == nil {
		return ClaimPolicy{}, false
	}
	return *descriptor.ClaimPolicy, true
}

// LookupAuthCapability returns tracker-owned auth support metadata.
func (r *Registry) LookupAuthCapability(tracker string) (api.TrackerAuthCapability, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.AuthCapability == nil {
		return api.TrackerAuthCapability{}, false
	}
	capability := *descriptor.AuthCapability
	capability.Notes = append([]string(nil), capability.Notes...)
	return capability, true
}

// LookupAuthSessionResolver returns tracker-owned remote auth behavior.
func (r *Registry) LookupAuthSessionResolver(tracker string) (AuthSessionResolver, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.AuthResolver, ok && descriptor.AuthResolver != nil
}

// LookupImageHostPolicy returns tracker-owned accepted image hosts.
func (r *Registry) LookupImageHostPolicy(tracker string) (ImageHostPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.ImageHost == nil {
		return ImageHostPolicy{}, false
	}
	policy := *descriptor.ImageHost
	policy.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	return policy, true
}

// LookupDataPolicy returns tracker-owned lookup orchestration policy.
func (r *Registry) LookupDataPolicy(tracker string) (DataLookupPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.DataPolicy == nil {
		return DataLookupPolicy{}, false
	}
	return *descriptor.DataPolicy, true
}

// LookupKind returns the registered tracker protocol family.
func (r *Registry) LookupKind(tracker string) (Kind, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.Kind, ok && descriptor.Kind != KindUnknown
}

// NamesByKind returns registered tracker names of kind in deterministic order.
func (r *Registry) NamesByKind(kind Kind) []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0)
	for name, descriptor := range r.descriptors {
		if descriptor.Kind == kind {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// LookupClaimCheckerFactory returns tracker-owned claim-check construction.
func (r *Registry) LookupClaimCheckerFactory(tracker string) (ClaimCheckerFactory, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.ClaimFactory, ok && descriptor.ClaimFactory != nil
}

// LookupAudioPolicy returns tracker-specific multi-language constraints.
func (r *Registry) LookupAudioPolicy(tracker string) (AudioPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.AudioPolicy == nil {
		return AudioPolicy{}, false
	}
	policy := *descriptor.AudioPolicy
	policy.AllowedLanguages = append([]string(nil), policy.AllowedLanguages...)
	return policy, true
}

// LookupDupePolicy returns tracker-specific duplicate comparison semantics.
func (r *Registry) LookupDupePolicy(tracker string) (DupePolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.DupePolicy == nil {
		return DupePolicy{}, false
	}
	return *descriptor.DupePolicy, true
}

// LookupUploadArtifactPolicy returns tracker torrent personalization fields.
func (r *Registry) LookupUploadArtifactPolicy(tracker string) (UploadArtifactPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.UploadArtifact == nil {
		return UploadArtifactPolicy{}, false
	}
	return *descriptor.UploadArtifact, true
}

// LookupMetadataPolicy returns tracker-owned metadata requirements.
func (r *Registry) LookupMetadataPolicy(tracker string) (TrackerMetadataPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.Metadata == nil {
		return TrackerMetadataPolicy{}, false
	}
	return cloneMetadataPolicy(*descriptor.Metadata), true
}

// LookupBannedGroups returns tracker-owned static banned release groups.
func (r *Registry) LookupBannedGroups(tracker string) ([]string, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || len(descriptor.BannedGroups) == 0 {
		return nil, false
	}
	return append([]string(nil), descriptor.BannedGroups...), true
}

// LookupBannedGroupPolicy returns tracker-owned dynamic blacklist behavior.
func (r *Registry) LookupBannedGroupPolicy(tracker string) (BannedGroupPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.BannedPolicy == nil {
		return BannedGroupPolicy{}, false
	}
	return *descriptor.BannedPolicy, true
}

// LookupDataFactory returns tracker's runtime metadata lookup factory.
func (r *Registry) LookupDataFactory(tracker string) (DataLookupFactory, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.DataFactory, ok && descriptor.DataFactory != nil
}

// DataLookupConfigured reports whether tracker-owned lookup credentials are ready.
func (r *Registry) DataLookupConfigured(tracker string, cfg config.Config) (bool, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok {
		return false, false
	}
	provider, ok := descriptor.Definition.(DataLookupConfigProvider)
	if !ok {
		return false, false
	}
	return provider.DataLookupConfigured(cfg), true
}

// LookupArtifactPolicy returns tracker's torrent artifact constraints.
func (r *Registry) LookupArtifactPolicy(tracker string) (ArtifactPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.Artifact == nil {
		return ArtifactPolicy{}, false
	}
	return *descriptor.Artifact, true
}

// RegisterDescriptor validates and registers a tracker and its capabilities.
func (r *Registry) RegisterDescriptor(descriptor Descriptor) error {
	def := descriptor.Definition
	if def == nil {
		return errors.New("trackers: definition is nil")
	}
	name := strings.ToUpper(strings.TrimSpace(descriptor.Name))
	if name == "" {
		return errors.New("trackers: definition has empty name")
	}
	definitionName := strings.ToUpper(strings.TrimSpace(def.Name()))
	if definitionName != name {
		return fmt.Errorf("trackers: descriptor name %s does not match definition name %s", name, definitionName)
	}
	if _, exists := r.descriptors[name]; exists {
		return fmt.Errorf("trackers: definition already registered: %s", name)
	}
	if descriptor.AuthCapability != nil {
		authName := strings.ToUpper(strings.TrimSpace(descriptor.AuthCapability.TrackerID))
		if authName != name {
			return fmt.Errorf("trackers: auth capability name %s does not match definition name %s", authName, name)
		}
		descriptor.AuthCapability.TrackerID = name
		descriptor.AuthCapability.Notes = append([]string(nil), descriptor.AuthCapability.Notes...)
	}
	descriptor.Name = name
	if descriptor.Kind == KindUnknown {
		descriptor.Kind = KindNonUnit3D
		if provider, ok := def.(KindProvider); ok {
			descriptor.Kind = provider.TrackerKind()
		}
	}
	descriptor.BaseURL = strings.TrimRight(strings.TrimSpace(descriptor.BaseURL), "/")
	r.descriptors[name] = descriptor
	return nil
}

// LookupBaseURL returns tracker's registered default endpoint.
func (r *Registry) LookupBaseURL(tracker string) (string, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.BaseURL, ok && descriptor.BaseURL != ""
}

// Lookup returns the definition registered for tracker using a case-insensitive name.
func (r *Registry) Lookup(tracker string) (Definition, bool) {
	if r == nil {
		return nil, false
	}
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return nil, false
	}
	descriptor, ok := r.descriptors[key]
	return descriptor.Definition, ok
}

// LookupDescriptor returns all registered capabilities for tracker.
func (r *Registry) LookupDescriptor(tracker string) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	descriptor, ok := r.descriptors[strings.ToUpper(strings.TrimSpace(tracker))]
	return descriptor, ok
}

// LookupRules returns tracker's registered rule capability.
func (r *Registry) LookupRules(tracker string) (ruletypes.RuleSet, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.Rules == nil {
		return ruletypes.RuleSet{}, false
	}
	return *descriptor.Rules, true
}

// Names returns normalized tracker names in deterministic order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.descriptors))
	for name := range r.descriptors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
