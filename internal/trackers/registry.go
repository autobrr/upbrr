// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// Registry stores construction-time tracker definitions and typed capabilities.
// Names are normalized for case-insensitive lookup; pointer-valued capabilities
// exposed through [Registry.LookupDescriptor] remain registry-owned and must be
// treated as read-only.
type Registry struct {
	descriptors map[string]Descriptor
	priority    []string
}

// SetPriorityOrder replaces the curated preference order with a lower-case,
// stable, deduplicated copy.
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
	for _, name := range r.NamesByFamily(FamilyUnit3D) {
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
		descriptor.DisplayName = def.Name()
		descriptor.Family = FamilyStandalone
		if provider, ok := def.(BaseURLProvider); ok {
			descriptor.BaseURL = strings.TrimSpace(provider.DefaultBaseURL())
		}
		if provider, ok := def.(FamilyProvider); ok {
			descriptor.Family = provider.TrackerFamily()
		}
		descriptor.ProjectorVersion = strings.ToLower(string(descriptor.Family)) + "-v2"
		if provider, ok := def.(ReleaseNamePolicyProvider); ok {
			descriptor.ReleaseNamePolicy = provider.ReleaseNamePolicy()
		} else {
			descriptor.ReleaseNamePolicy = defaultReleaseNamePolicy(descriptor.Family)
		}
		if provider, ok := def.(LocalizedMetadataProvider); ok {
			descriptor.MetadataLocale = strings.TrimSpace(provider.LocalizedMetadataLocale())
		}
		if provider, ok := def.(DescriptionGroupProvider); ok {
			descriptor.DescriptionGroup = strings.ToLower(strings.TrimSpace(provider.DescriptionGroup()))
		}
		if provider, ok := def.(UploadContentModeProvider); ok {
			descriptor.UploadContentMode = provider.UploadContentMode()
		}
		if provider, ok := def.(WorkflowMediaRequirementsProvider); ok {
			descriptor.WorkflowMedia = provider.WorkflowMediaRequirements()
		}
		descriptor.DataFactory, _ = def.(DataLookupFactory)
		descriptor.ClaimFactory, _ = def.(ClaimCheckerFactory)
		if provider, ok := def.(ClaimPolicyProvider); ok {
			descriptor.ClaimPolicy = provider.ClaimPolicy()
		}
		if provider, ok := def.(RuleProvider); ok {
			descriptor.Rules = provider.Rules()
		}
		if provider, ok := def.(ValidationPolicyProvider); ok {
			descriptor.Validation = provider.ValidationPolicy()
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
		if descriptor.DupePolicy == nil {
			descriptor.DupePolicy = compatibilityDupePolicy(descriptor.Name)
		}
		if provider, ok := def.(AudioPolicyProvider); ok {
			descriptor.AudioPolicy = provider.AudioPolicy()
		}
		if provider, ok := def.(ImageHostPolicyProvider); ok {
			descriptor.ImageHost = provider.ImageHostPolicy()
		}
		if provider, ok := def.(TorrentIdentityPolicyProvider); ok {
			descriptor.TorrentIdentity = provider.TorrentIdentityPolicy()
		} else if descriptor.Family == FamilyUnit3D && descriptor.BaseURL != "" {
			descriptor.TorrentIdentity = &TorrentIdentityPolicy{
				TrackerURLPatterns: []string{descriptor.BaseURL},
				CommentURLPatterns: []string{descriptor.BaseURL},
				DetailIDPattern:    `/(\d+)`,
			}
		}
		if provider, ok := def.(AuthSessionProvider); ok {
			descriptor.AuthResolver = provider.AuthSessionResolver()
		}
		if provider, ok := def.(AuthCapabilityDescriptorProvider); ok {
			descriptor.AuthCapability = provider.AuthCapabilityDescriptor()
		} else if provider, ok := def.(AuthCapabilityProvider); ok {
			capability := provider.AuthCapability()
			descriptor.AuthCapability = &capability
		}
		if provider, ok := def.(AuthPolicyProvider); ok {
			descriptor.AuthPolicy = provider.AuthPolicy()
		}
		if provider, ok := def.(AuthStateManagerProvider); ok {
			descriptor.AuthStateManager = provider.AuthStateManager()
		}
	}
	return r.RegisterDescriptor(descriptor)
}

func compatibilityDupePolicy(tracker string) *DupePolicy {
	return &DupePolicy{
		ID: strings.ToLower(strings.TrimSpace(tracker)) + "/duplicate-compat/v1",
		SearchScope: DupeSearchScope{
			MaxPages: 100,
		},
	}
}

// LookupTorrentIdentityPolicy returns tracker-owned torrent-client identity behavior.
func (r *Registry) LookupTorrentIdentityPolicy(tracker string) (TorrentIdentityPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.TorrentIdentity == nil {
		return TorrentIdentityPolicy{}, false
	}
	policy := *descriptor.TorrentIdentity
	policy.TrackerURLPatterns = append([]string(nil), policy.TrackerURLPatterns...)
	policy.CommentURLPatterns = append([]string(nil), policy.CommentURLPatterns...)
	return policy, true
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

// LookupAuthPolicy returns tracker-owned auth readiness semantics.
func (r *Registry) LookupAuthPolicy(tracker string) (AuthPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.AuthPolicy == nil {
		return AuthPolicy{}, false
	}
	return *descriptor.AuthPolicy, true
}

// ResolveEffectiveAuthRequirements returns secret-free requirements for the
// tracker's effective config mode.
func (r *Registry) ResolveEffectiveAuthRequirements(
	tracker string,
	cfg config.Config,
	trackerConfig config.TrackerConfig,
) (EffectiveAuthRequirements, bool) {
	policy, ok := r.LookupAuthPolicy(tracker)
	if !ok || policy.ResolveRequirements == nil {
		return EffectiveAuthRequirements{}, false
	}
	return policy.ResolveRequirements(cfg, trackerConfig).Clone(), true
}

// LookupAuthStateManager returns tracker-owned persisted auth cleanup.
func (r *Registry) LookupAuthStateManager(tracker string) (AuthStateManager, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.AuthStateManager, ok && descriptor.AuthStateManager != nil
}

// LookupImageHostPolicy returns tracker-owned accepted image hosts.
func (r *Registry) LookupImageHostPolicy(tracker string) (ImageHostPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.ImageHost == nil {
		return ImageHostPolicy{}, false
	}
	policy := *descriptor.ImageHost
	policy.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	policy.OwnedHosts = append([]string(nil), policy.OwnedHosts...)
	return policy, true
}

// LookupUploadContentMode returns the tracker-owned shared content workflow.
func (r *Registry) LookupUploadContentMode(tracker string) (UploadContentMode, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.UploadContentMode, ok && descriptor.UploadContentMode.Valid()
}

// OwnerForImageHost returns the tracker that owns a private image host.
func (r *Registry) OwnerForImageHost(host string) string {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" {
		return ""
	}
	for _, tracker := range r.Names() {
		policy, ok := r.LookupImageHostPolicy(tracker)
		if !ok {
			continue
		}
		for _, ownedHost := range policy.OwnedHosts {
			if strings.EqualFold(strings.TrimSpace(ownedHost), normalized) {
				return tracker
			}
		}
	}
	return ""
}

// LookupDataPolicy returns tracker-owned lookup orchestration policy.
func (r *Registry) LookupDataPolicy(tracker string) (DataLookupPolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.DataPolicy == nil {
		return DataLookupPolicy{}, false
	}
	return *descriptor.DataPolicy, true
}

// LookupFamily returns the registered tracker protocol family.
func (r *Registry) LookupFamily(tracker string) (Family, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.Family, ok && descriptor.Family != FamilyUnknown
}

// NamesByFamily returns registered tracker names of family in deterministic order.
func (r *Registry) NamesByFamily(family Family) []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0)
	for name, descriptor := range r.descriptors {
		if descriptor.Family == family {
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
	return cloneDupePolicy(*descriptor.DupePolicy), true
}

func cloneDupePolicy(policy DupePolicy) DupePolicy {
	policy.SlotDimensions = append([]DupeDimension(nil), policy.SlotDimensions...)
	policy.OptionalSlotDimensions = append([]DupeDimension(nil), policy.OptionalSlotDimensions...)
	policy.CompleteSlotDimensions = append([]DupeDimension(nil), policy.CompleteSlotDimensions...)
	policy.RequiredDimensions = append([]DupeDimension(nil), policy.RequiredDimensions...)
	policy.SuppressGeneralCoexistence = append([]DupeDimension(nil), policy.SuppressGeneralCoexistence...)
	policy.CoexistenceRules = cloneDupeRules(policy.CoexistenceRules)
	policy.PrecedenceRules = cloneDupeRules(policy.PrecedenceRules)
	policy.ManualReviewRules = cloneDupeRules(policy.ManualReviewRules)
	policy.SetRules = cloneDupeSetRules(policy.SetRules)
	policy.SizeVarianceResolutions = append([]string(nil), policy.SizeVarianceResolutions...)
	policy.SizeVarianceTypes = append([]string(nil), policy.SizeVarianceTypes...)
	return policy
}

func cloneDupeSetRules(rules []DupeSetRule) []DupeSetRule {
	result := make([]DupeSetRule, len(rules))
	for index, rule := range rules {
		rule.TargetPredicates = cloneDupeSetPredicates(rule.TargetPredicates)
		rule.CandidatePredicates = cloneDupeSetPredicates(rule.CandidatePredicates)
		rule.CapacityOverrides = append([]DupeSetCapacityOverride(nil), rule.CapacityOverrides...)
		for overrideIndex := range rule.CapacityOverrides {
			rule.CapacityOverrides[overrideIndex].CandidatePredicates = cloneDupeSetPredicates(
				rule.CapacityOverrides[overrideIndex].CandidatePredicates,
			)
		}
		result[index] = rule
	}
	return result
}

func cloneDupeSetPredicates(predicates []DupeSetPredicate) []DupeSetPredicate {
	result := make([]DupeSetPredicate, len(predicates))
	for index, predicate := range predicates {
		predicate.Values = append([]string(nil), predicate.Values...)
		predicate.ExcludedValues = append([]string(nil), predicate.ExcludedValues...)
		result[index] = predicate
	}
	return result
}

func cloneDupeRules(rules []DupeRule) []DupeRule {
	result := make([]DupeRule, len(rules))
	for index, rule := range rules {
		conditions := rule.Conditions
		rule.Conditions = make([]DupeCondition, len(conditions))
		for conditionIndex, condition := range conditions {
			condition.TargetValues = append([]string(nil), condition.TargetValues...)
			condition.CandidateValues = append([]string(nil), condition.CandidateValues...)
			rule.Conditions[conditionIndex] = condition
		}
		result[index] = rule
	}
	return result
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

// RegisterDescriptor validates and normalizes one construction-time descriptor.
// It rejects duplicate names, non-HTTPS endpoints, invalid families/content
// modes, malformed ID patterns, and conflicting private image-host ownership.
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
	if descriptor.WorkflowMedia != nil {
		if descriptor.WorkflowMedia.DVDMenuCount < 0 {
			return fmt.Errorf("trackers: definition %s has negative DVD menu requirement", name)
		}
		workflowMedia := *descriptor.WorkflowMedia
		descriptor.WorkflowMedia = &workflowMedia
	}
	descriptor.Name = name
	descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
	if descriptor.DisplayName == "" {
		descriptor.DisplayName = name
	}
	descriptor.ProjectorVersion = strings.TrimSpace(descriptor.ProjectorVersion)
	aliases := make([]string, 0, len(descriptor.Aliases))
	seenAliases := make(map[string]struct{}, len(descriptor.Aliases))
	for _, alias := range descriptor.Aliases {
		alias = strings.ToUpper(strings.TrimSpace(alias))
		if alias == "" || alias == name {
			return fmt.Errorf("trackers: definition %s has invalid alias", name)
		}
		if _, ok := seenAliases[alias]; ok {
			continue
		}
		seenAliases[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	descriptor.Aliases = aliases
	for existingName, existing := range r.descriptors {
		if slices.Contains(existing.Aliases, name) {
			return fmt.Errorf("trackers: definition name %s conflicts with alias owned by %s", name, existingName)
		}
		for _, alias := range descriptor.Aliases {
			if alias == existingName || slices.Contains(existing.Aliases, alias) {
				return fmt.Errorf("trackers: definition %s alias %s conflicts with %s", name, alias, existingName)
			}
		}
	}
	if descriptor.Family == FamilyUnknown {
		descriptor.Family = FamilyStandalone
		if provider, ok := def.(FamilyProvider); ok {
			descriptor.Family = provider.TrackerFamily()
		}
	}
	if descriptor.ReleaseNamePolicy.Resolver == nil {
		if provider, ok := def.(ReleaseNamePolicyProvider); ok {
			descriptor.ReleaseNamePolicy = provider.ReleaseNamePolicy()
		} else {
			descriptor.ReleaseNamePolicy = defaultReleaseNamePolicy(descriptor.Family)
		}
	}
	descriptor.ReleaseNamePolicy.ID = strings.TrimSpace(descriptor.ReleaseNamePolicy.ID)
	if err := validateReleaseNamePolicy(descriptor.ReleaseNamePolicy); err != nil {
		return fmt.Errorf("trackers: definition %s has invalid release-name policy: %w", name, err)
	}
	if descriptor.ProjectorVersion == "" {
		descriptor.ProjectorVersion = strings.ToLower(string(descriptor.Family)) + "-v2"
	}
	if descriptor.ProjectorVersion == "" {
		return fmt.Errorf("trackers: definition %s has no versioned release projector", name)
	}
	if descriptor.Validation.ID == "" && descriptor.Validation.Check == nil {
		if provider, ok := def.(ValidationPolicyProvider); ok {
			descriptor.Validation = provider.ValidationPolicy()
		} else {
			descriptor.Validation = NoExtraValidationPolicy(strings.ToLower(name) + "-no-extra-validation-v1")
		}
	}
	descriptor.Validation.ID = strings.TrimSpace(descriptor.Validation.ID)
	if err := validateValidationPolicy(descriptor.Validation); err != nil {
		return fmt.Errorf("trackers: definition %s has invalid validation policy: %w", name, err)
	}
	if strings.TrimSpace(descriptor.BaseURL) == "" {
		if provider, ok := def.(BaseURLProvider); ok {
			descriptor.BaseURL = provider.DefaultBaseURL()
		}
	}
	descriptor.BaseURL = strings.TrimRight(strings.TrimSpace(descriptor.BaseURL), "/")
	if descriptor.BaseURL == "" {
		return fmt.Errorf("trackers: definition %s has empty base URL", name)
	}
	endpoint, err := url.Parse(descriptor.BaseURL)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" {
		return fmt.Errorf("trackers: definition %s has invalid HTTPS base URL %q", name, descriptor.BaseURL)
	}
	if descriptor.Family != FamilyUnit3D && descriptor.Family != FamilyAZFamily && descriptor.Family != FamilyStandalone {
		return fmt.Errorf("trackers: definition %s has invalid family %q", name, descriptor.Family)
	}
	if !descriptor.UploadContentMode.Valid() {
		if provider, ok := def.(UploadContentModeProvider); ok {
			descriptor.UploadContentMode = provider.UploadContentMode()
		}
	}
	if !descriptor.UploadContentMode.Valid() {
		return fmt.Errorf("trackers: definition %s has invalid upload content mode %q", name, descriptor.UploadContentMode)
	}
	if descriptor.TorrentIdentity != nil {
		policy := *descriptor.TorrentIdentity
		policy.TrackerURLPatterns = normalizePolicyPatterns(policy.TrackerURLPatterns)
		policy.CommentURLPatterns = normalizePolicyPatterns(policy.CommentURLPatterns)
		policy.DetailIDPattern = strings.TrimSpace(policy.DetailIDPattern)
		if policy.DetailIDPattern != "" {
			compiled, compileErr := regexp.Compile(policy.DetailIDPattern)
			if compileErr != nil || compiled.NumSubexp() < 1 {
				return fmt.Errorf("trackers: definition %s has invalid torrent ID pattern %q", name, policy.DetailIDPattern)
			}
		}
		policy.WorkingTrackerID = strings.TrimSpace(policy.WorkingTrackerID)
		descriptor.TorrentIdentity = &policy
	}
	if descriptor.ImageHost != nil {
		policy := *descriptor.ImageHost
		policy.AllowedHosts = normalizePolicyPatterns(policy.AllowedHosts)
		policy.OwnedHosts = normalizePolicyPatterns(policy.OwnedHosts)
		for _, ownedHost := range policy.OwnedHosts {
			for registeredName, registered := range r.descriptors {
				if registered.ImageHost == nil {
					continue
				}
				for _, existingHost := range registered.ImageHost.OwnedHosts {
					if strings.EqualFold(existingHost, ownedHost) {
						return fmt.Errorf("trackers: image host %s is owned by both %s and %s", ownedHost, registeredName, name)
					}
				}
			}
		}
		descriptor.ImageHost = &policy
	}
	if descriptor.DupePolicy != nil {
		policy := cloneDupePolicy(*descriptor.DupePolicy)
		if err := validateDupePolicy(policy); err != nil {
			return fmt.Errorf("trackers: definition %s has invalid duplicate policy: %w", name, err)
		}
		descriptor.DupePolicy = &policy
	}
	r.descriptors[name] = descriptor
	return nil
}

func validateDupePolicy(policy DupePolicy) error {
	if strings.TrimSpace(policy.ID) == "" {
		return errors.New("policy ID is empty")
	}
	isCompatibility := strings.Contains(strings.ToLower(policy.ID), "/duplicate-compat/")
	if !isCompatibility && (len(policy.SlotDimensions) > 0 || len(policy.OptionalSlotDimensions) > 0 || len(policy.CompleteSlotDimensions) > 0 ||
		len(policy.RequiredDimensions) > 0 || len(policy.SuppressGeneralCoexistence) > 0 || len(policy.CoexistenceRules) > 0 ||
		len(policy.PrecedenceRules) > 0 || len(policy.SetRules) > 0 || policy.SizeVariancePercent > 0) {
		if strings.TrimSpace(policy.EvidenceID) == "" {
			return errors.New("automatic policy has no evidence ID")
		}
	}
	seenRuleIDs := make(map[string]struct{})
	groups := [][]DupeRule{policy.CoexistenceRules, policy.PrecedenceRules, policy.ManualReviewRules}
	for _, rules := range groups {
		for _, rule := range rules {
			ruleID := strings.TrimSpace(rule.ID)
			if ruleID == "" {
				return errors.New("rule ID is empty")
			}
			if _, exists := seenRuleIDs[ruleID]; exists {
				return fmt.Errorf("duplicate rule ID %q", ruleID)
			}
			seenRuleIDs[ruleID] = struct{}{}
			if strings.TrimSpace(rule.Relation) == "" && !rule.RequiresManualStep {
				return fmt.Errorf("rule %q has no relation", ruleID)
			}
			if !rule.RequiresManualStep && !isCompatibility &&
				strings.TrimSpace(firstPolicyEvidenceID(rule.EvidenceID, policy.EvidenceID)) == "" {
				return fmt.Errorf("automatic rule %q has no evidence ID", ruleID)
			}
		}
	}
	for _, rule := range policy.SetRules {
		ruleID := strings.TrimSpace(rule.ID)
		if ruleID == "" {
			return errors.New("set rule ID is empty")
		}
		if _, exists := seenRuleIDs[ruleID]; exists {
			return fmt.Errorf("duplicate rule ID %q", ruleID)
		}
		seenRuleIDs[ruleID] = struct{}{}
		if strings.TrimSpace(firstPolicyEvidenceID(rule.EvidenceID, policy.EvidenceID)) == "" {
			return fmt.Errorf("set rule %q has no evidence ID", ruleID)
		}
		if len(rule.TargetPredicates) == 0 || len(rule.CandidatePredicates) == 0 {
			return fmt.Errorf("set rule %q has no target or candidate predicates", ruleID)
		}
		if rule.Capacity <= 0 {
			return fmt.Errorf("set rule %q has invalid capacity", ruleID)
		}
		if rule.MinimumSizeSeparationPercent < 0 || rule.MinimumSizeSeparationPercent >= 100 {
			return fmt.Errorf("set rule %q has invalid size separation", ruleID)
		}
		for _, override := range rule.CapacityOverrides {
			if override.Capacity <= 0 || override.Capacity > rule.Capacity || len(override.CandidatePredicates) == 0 {
				return fmt.Errorf("set rule %q has invalid capacity override", ruleID)
			}
		}
	}
	return nil
}

func firstPolicyEvidenceID(ruleEvidenceID string, policyEvidenceID string) string {
	if value := strings.TrimSpace(ruleEvidenceID); value != "" {
		return value
	}
	return strings.TrimSpace(policyEvidenceID)
}

func normalizePolicyPatterns(patterns []string) []string {
	normalized := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.ToLower(strings.TrimSpace(pattern))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
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
	if strings.TrimSpace(tracker) == "" {
		return nil, false
	}
	descriptor, ok := r.LookupDescriptor(tracker)
	return descriptor.Definition, ok
}

// LookupDescriptor returns a shallow registry-owned capability snapshot. Callers
// needing mutable policy data must use the typed lookup methods, which copy
// mutable slices where required.
func (r *Registry) LookupDescriptor(tracker string) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	wanted := strings.ToUpper(strings.TrimSpace(tracker))
	descriptor, ok := r.descriptors[wanted]
	if ok {
		descriptor.Aliases = slices.Clone(descriptor.Aliases)
		return descriptor, true
	}
	for _, candidate := range r.descriptors {
		if slices.Contains(candidate.Aliases, wanted) {
			candidate.Aliases = slices.Clone(candidate.Aliases)
			return candidate, true
		}
	}
	return Descriptor{}, false
}

// LookupRules returns tracker's registered rule capability.
func (r *Registry) LookupRules(tracker string) (RuleSet, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	if !ok || descriptor.Rules == nil {
		return RuleSet{}, false
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
