// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	"github.com/autobrr/upbrr/pkg/api"
)

type imageHostPolicy struct {
	allowed     []string
	uploadHosts []string
	preferred   []string
	required    bool
	fallbackOK  bool
}

// ImageUploadTarget identifies one host upload required by a tracker set.
type ImageUploadTarget struct {
	// Host is the normalized uploader name.
	Host string
	// UsageScope groups uploads that may share one hosted variant.
	UsageScope string
	// Trackers lists the trackers whose policies require this target.
	Trackers []string
}

type imageUploadPolicyTarget struct {
	tracker    string
	policy     imageHostPolicy
	candidates []string
}

func policyForTracker(tracker string, trackerCfg config.TrackerConfig) imageHostPolicy {
	return policyForTrackerWithRegistry(nil, tracker, trackerCfg)
}

func policyForTrackerWithRegistry(registry *Registry, tracker string, trackerCfg config.TrackerConfig) imageHostPolicy {
	if declared, ok := registry.LookupImageHostPolicy(tracker); ok {
		if declared.DisableWithoutRehost && !trackerCfg.ImgRehost {
			return imageHostPolicy{}
		}
		if declared.DisableWithoutAPI && strings.TrimSpace(trackerCfg.ImgAPI) == "" {
			return imageHostPolicy{}
		}
		return newImageHostPolicy(declared.AllowedHosts...)
	}
	return policyFromShared(imagehostpolicy.ForTracker(tracker, trackerCfg.ImgRehost, trackerCfg.ImgAPI))
}

func policyForTrackerWithConfigAndRegistry(registry *Registry, tracker string, appCfg config.Config, trackerCfg config.TrackerConfig) imageHostPolicy {
	if host, enabled := conditionalImageHost(registry, appCfg, tracker, trackerCfg); enabled {
		return newImageHostPolicy(host)
	}
	return policyForTrackerWithRegistry(registry, tracker, trackerCfg)
}

func applyImageHostOverrides(tracker string, policy imageHostPolicy, overrides api.ImageHostOverrides) (imageHostPolicy, error) {
	if overrides.PreferredHost == nil {
		return policy, nil
	}
	host := strings.ToLower(strings.TrimSpace(*overrides.PreferredHost))
	if host == "" {
		return policy, nil
	}
	if owner := trackerForOwnedHost(host); owner != "" && !strings.EqualFold(owner, tracker) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s image host override %q is owned by %s", strings.TrimSpace(tracker), host, owner)
	}
	if !supportedUploadImageHost(host) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s image host override %q is unsupported", strings.TrimSpace(tracker), host)
	}
	if len(policy.allowed) == 0 {
		return newPreferredImageHostPolicy(host), nil
	}
	if !hostAllowed(host, policy.allowed) {
		return imageHostPolicy{}, fmt.Errorf(
			"trackers: %s image host override %q is not allowed (allowed: %s)",
			strings.TrimSpace(tracker),
			host,
			strings.Join(policy.allowed, ", "),
		)
	}
	policy.preferred = prependHost(host, policy.preferred)
	policy.fallbackOK = true
	return policy, nil
}

func resolveImageHostPolicy(tracker string, trackerCfg config.TrackerConfig, overrides api.ImageHostOverrides) (imageHostPolicy, error) {
	return resolveImageHostPolicyWithRegistry(nil, tracker, trackerCfg, overrides)
}

func resolveImageHostPolicyWithRegistry(
	registry *Registry,
	tracker string,
	trackerCfg config.TrackerConfig,
	overrides api.ImageHostOverrides,
) (imageHostPolicy, error) {
	policy := policyForTrackerWithRegistry(registry, tracker, trackerCfg)
	host := strings.ToLower(strings.TrimSpace(trackerCfg.ImageHost))
	if host == "" {
		return applyImageHostOverrides(tracker, policy, overrides)
	}
	if !supportedUploadImageHost(host) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s configured image_host %q is unsupported", strings.TrimSpace(tracker), trackerCfg.ImageHost)
	}
	if owner := trackerForOwnedHost(host); owner != "" && !strings.EqualFold(owner, tracker) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s configured image_host %q is owned by %s", strings.TrimSpace(tracker), trackerCfg.ImageHost, owner)
	}
	if len(policy.allowed) > 0 && !hostAllowed(host, policy.allowed) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s configured image_host %q is not allowed", strings.TrimSpace(tracker), trackerCfg.ImageHost)
	}
	if len(policy.allowed) == 0 {
		return newPreferredImageHostPolicy(host), nil
	}
	policy.preferred = prependHost(host, policy.preferred)
	policy.fallbackOK = true
	return policy, nil
}

func resolveImageHostPolicyForMetadataWithRegistry(
	registry *Registry,
	tracker string,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	overrides api.ImageHostOverrides,
) (imageHostPolicy, error) {
	host := strings.ToLower(strings.TrimSpace(trackerCfg.ImageHost))
	if conditionalHost, enabled := conditionalImageHost(registry, appCfg, tracker, trackerCfg); host == conditionalHost && !enabled {
		trackerCfg.ImageHost = ""
	}
	if strings.TrimSpace(trackerCfg.ImageHost) != "" || overrides.PreferredHost != nil {
		policy, err := resolveImageHostPolicyWithRegistry(registry, tracker, trackerCfg, overrides)
		if err != nil {
			return imageHostPolicy{}, err
		}
		return withUnrestrictedImageHostFallbacks(registry, tracker, policy, appCfg), nil
	}
	policy := policyForTrackerWithConfigAndRegistry(registry, tracker, appCfg, trackerCfg)
	policy, err := applyImageHostOverrides(tracker, policy, overrides)
	if err != nil {
		return imageHostPolicy{}, err
	}
	return withUnrestrictedImageHostFallbacks(registry, tracker, policy, appCfg), nil
}

// PreferredImageUploadHost resolves the preferred upload host for tracker.
func PreferredImageUploadHost(tracker string, trackerCfg config.TrackerConfig, overrides api.ImageHostOverrides) (string, error) {
	return PreferredImageUploadHostWithRegistry(nil, tracker, trackerCfg, overrides)
}

// PreferredImageUploadHostWithRegistry resolves a preferred host from the tracker's registered policy.
func PreferredImageUploadHostWithRegistry(
	registry *Registry,
	tracker string,
	trackerCfg config.TrackerConfig,
	overrides api.ImageHostOverrides,
) (string, error) {
	policy, err := resolveImageHostPolicyWithRegistry(registry, tracker, trackerCfg, overrides)
	if err != nil {
		return "", err
	}
	return preferredHost(policy), nil
}

// RequiredImageUploadTargets returns mandatory upload targets for trackerNames.
func RequiredImageUploadTargets(appCfg config.Config, trackerNames []string, overrides api.ImageHostOverrides) ([]ImageUploadTarget, error) {
	targets := make([]ImageUploadTarget, 0, len(trackerNames))
	seen := make(map[string]int, len(trackerNames))
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		trackerCfg := trackerConfigForImageHostPolicy(appCfg, name)
		policy, err := resolveImageHostPolicy(name, trackerCfg, overrides)
		if err != nil {
			return nil, err
		}
		host := preferredHost(policy)
		if host == "" {
			continue
		}
		scope := usageScopeForHost(host)
		// Use a null-byte separator to build an unambiguous host+scope dedupe key.
		// Host/scope values are expected not to contain \x00, avoiding concat collisions.
		key := host + "\x00" + scope
		if idx, ok := seen[key]; ok {
			targets[idx].Trackers = appendUniqueTracker(targets[idx].Trackers, name)
			continue
		}
		seen[key] = len(targets)
		targets = append(targets, ImageUploadTarget{
			Host:       host,
			UsageScope: scope,
			Trackers:   []string{name},
		})
	}
	return targets, nil
}

// ConfiguredImageUploadTargets returns enabled upload targets accepted by trackerNames.
func ConfiguredImageUploadTargets(appCfg config.Config, trackerNames []string) ([]ImageUploadTarget, error) {
	targets := make([]ImageUploadTarget, 0, len(trackerNames))
	seen := make(map[string]int, len(trackerNames))
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		trackerCfg := trackerConfigForImageHostPolicy(appCfg, name)
		if strings.TrimSpace(trackerCfg.ImageHost) == "" {
			continue
		}
		policy, err := resolveImageHostPolicy(name, trackerCfg, api.ImageHostOverrides{})
		if err != nil {
			return nil, err
		}
		host := preferredHost(policy)
		if host == "" {
			continue
		}
		scope := usageScopeForHost(host)
		// Use a null-byte separator to build an unambiguous host+scope dedupe key.
		// Host/scope values are expected not to contain \x00, avoiding concat collisions.
		key := host + "\x00" + scope
		if idx, ok := seen[key]; ok {
			targets[idx].Trackers = appendUniqueTracker(targets[idx].Trackers, name)
			continue
		}
		seen[key] = len(targets)
		targets = append(targets, ImageUploadTarget{
			Host:       host,
			UsageScope: scope,
			Trackers:   []string{name},
		})
	}
	return targets, nil
}

// NeededImageUploadTargets returns targets not satisfied by selectedHost.
func NeededImageUploadTargets(appCfg config.Config, trackerNames []string, selectedHost string) ([]ImageUploadTarget, error) {
	return neededImageUploadTargets(nil, appCfg, trackerNames, selectedHost, nil, nil)
}

// NeededImageUploadTargetsForMetadata returns unsatisfied targets after considering metadata images.
func NeededImageUploadTargetsForMetadata(
	appCfg config.Config,
	trackerNames []string,
	selectedHost string,
	meta api.UploadSubject,
) ([]ImageUploadTarget, error) {
	return NeededImageUploadTargetsForMetadataWithRegistry(nil, appCfg, trackerNames, selectedHost, meta)
}

// NeededImageUploadTargetsForMetadataWithRegistry resolves image upload targets from tracker-owned policies.
func NeededImageUploadTargetsForMetadataWithRegistry(
	registry *Registry,
	appCfg config.Config,
	trackerNames []string,
	selectedHost string,
	meta api.UploadSubject,
) ([]ImageUploadTarget, error) {
	return neededImageUploadTargets(registry, appCfg, trackerNames, selectedHost, nil, &meta)
}

// NeededImageUploadTargetsExcluding returns unsatisfied targets except excluded hosts.
func NeededImageUploadTargetsExcluding(appCfg config.Config, trackerNames []string, selectedHost string, excludedHosts []string) ([]ImageUploadTarget, error) {
	excluded := make(map[string]struct{}, len(excludedHosts))
	for _, host := range excludedHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" {
			excluded[normalized] = struct{}{}
		}
	}
	return neededImageUploadTargets(nil, appCfg, trackerNames, selectedHost, excluded, nil)
}

// NeededImageUploadTargetsForMetadataExcluding combines metadata satisfaction with host exclusions.
func NeededImageUploadTargetsForMetadataExcluding(
	appCfg config.Config,
	trackerNames []string,
	selectedHost string,
	excludedHosts []string,
	meta api.UploadSubject,
) ([]ImageUploadTarget, error) {
	return NeededImageUploadTargetsForMetadataExcludingWithRegistry(nil, appCfg, trackerNames, selectedHost, excludedHosts, meta)
}

// NeededImageUploadTargetsForMetadataExcludingWithRegistry resolves fallback targets from tracker-owned policies.
func NeededImageUploadTargetsForMetadataExcludingWithRegistry(
	registry *Registry,
	appCfg config.Config,
	trackerNames []string,
	selectedHost string,
	excludedHosts []string,
	meta api.UploadSubject,
) ([]ImageUploadTarget, error) {
	excluded := make(map[string]struct{}, len(excludedHosts))
	for _, host := range excludedHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" {
			excluded[normalized] = struct{}{}
		}
	}
	return neededImageUploadTargets(registry, appCfg, trackerNames, selectedHost, excluded, &meta)
}

func neededImageUploadTargets(
	registry *Registry,
	appCfg config.Config,
	trackerNames []string,
	selectedHost string,
	excludedHosts map[string]struct{},
	meta *api.UploadSubject,
) ([]ImageUploadTarget, error) {
	selectedHost = strings.ToLower(strings.TrimSpace(selectedHost))
	userHosts := configuredImageUploadHosts(appCfg)
	targets := make([]ImageUploadTarget, 0, len(trackerNames)+1)
	seen := make(map[string]int, len(trackerNames)+1)

	addTarget := func(host string, tracker string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			return
		}
		if _, excluded := excludedHosts[host]; excluded {
			return
		}
		name := strings.ToUpper(strings.TrimSpace(tracker))
		scope := usageScopeForHost(host)
		key := host + "\x00" + scope
		if idx, ok := seen[key]; ok {
			targets[idx].Trackers = appendUniqueTracker(targets[idx].Trackers, name)
			return
		}
		seen[key] = len(targets)
		targets = append(targets, ImageUploadTarget{
			Host:       host,
			UsageScope: scope,
			Trackers:   []string{name},
		})
	}

	flexibleTargets := make([]imageUploadPolicyTarget, 0, len(trackerNames))
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		trackerCfg := trackerConfigForImageHostPolicy(appCfg, name)
		if strings.TrimSpace(trackerCfg.ImageHost) != "" {
			policy, err := resolveImageHostPolicyForTarget(registry, name, appCfg, trackerCfg, meta)
			if err != nil {
				return nil, err
			}
			if host := preferredHost(policy); host != "" {
				if _, excluded := excludedHosts[host]; !excluded {
					addTarget(host, name)
					continue
				}
			}
			flexibleTargets = append(flexibleTargets, imageUploadPolicyTarget{
				tracker:    name,
				policy:     policy,
				candidates: userHosts,
			})
			continue
		}

		policy := policyForTrackerForTarget(registry, name, appCfg, trackerCfg)
		candidates := imageUploadCandidatesForTracker(registry, appCfg, name, userHosts)
		candidates = appendOwnedPolicyUploadHosts(candidates, name, policy)
		flexibleTargets = append(flexibleTargets, imageUploadPolicyTarget{
			tracker:    name,
			policy:     policy,
			candidates: candidates,
		})
	}

	if selectedHost != "" && trackerForOwnedHost(selectedHost) == "" && hostInList(selectedHost, userHosts) {
		if _, excluded := excludedHosts[selectedHost]; !excluded && len(flexibleTargets) > 0 {
			usableForAllFlexible := true
			for _, target := range flexibleTargets {
				if !imageHostUsableForPolicy(target.tracker, selectedHost, target.policy) {
					usableForAllFlexible = false
					break
				}
			}
			if usableForAllFlexible {
				for _, target := range flexibleTargets {
					addTarget(selectedHost, target.tracker)
				}
				flexibleTargets = nil
			}
		}
	}

	assignFlexibleImageUploadTargets(flexibleTargets, excludedHosts, targets, addTarget)

	if len(targets) == 0 && selectedHost != "" && trackerForOwnedHost(selectedHost) == "" && hostInList(selectedHost, userHosts) {
		if _, excluded := excludedHosts[selectedHost]; excluded {
			return targets, nil
		}
		targets = append(targets, ImageUploadTarget{Host: selectedHost, UsageScope: globalImageUsageScope})
	}

	return targets, nil
}

func assignFlexibleImageUploadTargets(
	flexibleTargets []imageUploadPolicyTarget,
	excludedHosts map[string]struct{},
	targets []ImageUploadTarget,
	addTarget func(string, string),
) {
	unassigned := make([]imageUploadPolicyTarget, 0, len(flexibleTargets))
	for _, target := range flexibleTargets {
		if host, ok := existingImageUploadTargetHost(target.tracker, target.policy, target.candidates, targets); ok {
			addTarget(host, target.tracker)
			continue
		}
		unassigned = append(unassigned, target)
	}

	for len(unassigned) > 0 {
		host := bestImageUploadTargetHost(unassigned, excludedHosts)
		if host == "" {
			break
		}
		next := unassigned[:0]
		for _, target := range unassigned {
			if imageHostUsableForPolicy(target.tracker, host, target.policy) {
				addTarget(host, target.tracker)
				continue
			}
			next = append(next, target)
		}
		unassigned = next
	}
}

func existingImageUploadTargetHost(tracker string, policy imageHostPolicy, candidates []string, targets []ImageUploadTarget) (string, bool) {
	for _, target := range targets {
		if !hostInList(target.Host, candidates) {
			continue
		}
		if imageHostUsableForPolicy(tracker, target.Host, policy) {
			return target.Host, true
		}
	}
	return "", false
}

func bestImageUploadTargetHost(targets []imageUploadPolicyTarget, excludedHosts map[string]struct{}) string {
	rankings := make(map[string]imageUploadHostRanking, len(targets))
	for _, target := range targets {
		for idx, host := range candidateImageUploadTargetHosts(target.tracker, target.policy, target.candidates, excludedHosts) {
			ranking := rankings[host]
			ranking.host = host
			ranking.count++
			ranking.preference += idx
			rankings[host] = ranking
		}
	}

	var best imageUploadHostRanking
	for _, ranking := range rankings {
		if betterImageUploadHostRanking(ranking, best) {
			best = ranking
		}
	}
	return best.host
}

type imageUploadHostRanking struct {
	host       string
	count      int
	preference int
}

func betterImageUploadHostRanking(candidate imageUploadHostRanking, current imageUploadHostRanking) bool {
	if candidate.host == "" {
		return false
	}
	if current.host == "" {
		return true
	}
	if candidate.count != current.count {
		return candidate.count > current.count
	}
	if candidate.preference != current.preference {
		return candidate.preference < current.preference
	}
	return candidate.host < current.host
}

func candidateImageUploadTargetHosts(tracker string, policy imageHostPolicy, candidates []string, excludedHosts map[string]struct{}) []string {
	hostsByName := make(map[string]struct{}, len(candidates))
	for _, host := range candidates {
		normalizedHost := strings.ToLower(strings.TrimSpace(host))
		if _, excluded := excludedHosts[normalizedHost]; excluded {
			continue
		}
		if imageHostUsableForPolicy(tracker, normalizedHost, policy) {
			hostsByName[normalizedHost] = struct{}{}
		}
	}
	hosts := make([]string, 0, len(hostsByName))
	for _, host := range policy.preferred {
		normalizedHost := strings.ToLower(strings.TrimSpace(host))
		if _, ok := hostsByName[normalizedHost]; !ok {
			continue
		}
		hosts = append(hosts, normalizedHost)
		delete(hostsByName, normalizedHost)
	}
	for _, host := range candidates {
		normalizedHost := strings.ToLower(strings.TrimSpace(host))
		if _, ok := hostsByName[normalizedHost]; !ok {
			continue
		}
		hosts = append(hosts, normalizedHost)
		delete(hostsByName, normalizedHost)
	}
	return hosts
}

func resolveImageHostPolicyForTarget(
	registry *Registry,
	tracker string,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	meta *api.UploadSubject,
) (imageHostPolicy, error) {
	if meta == nil {
		policy, err := resolveImageHostPolicyWithRegistry(registry, tracker, trackerCfg, api.ImageHostOverrides{})
		if err != nil {
			return imageHostPolicy{}, err
		}
		return withUnrestrictedImageHostFallbacks(registry, tracker, policy, appCfg), nil
	}
	return resolveImageHostPolicyForMetadataWithRegistry(registry, tracker, appCfg, trackerCfg, api.ImageHostOverrides{})
}

func policyForTrackerForTarget(registry *Registry, tracker string, appCfg config.Config, trackerCfg config.TrackerConfig) imageHostPolicy {
	return policyForTrackerWithConfigAndRegistry(registry, tracker, appCfg, trackerCfg)
}

func imageUploadCandidatesForTracker(registry *Registry, appCfg config.Config, tracker string, userHosts []string) []string {
	candidates := append([]string(nil), userHosts...)
	if host, enabled := conditionalImageHost(registry, appCfg, tracker, trackerConfigForImageHostPolicy(appCfg, tracker)); enabled {
		candidates = appendUniqueHost(candidates, host)
	}
	return candidates
}

// appendOwnedPolicyUploadHosts adds upload-capable policy hosts owned by the
// target tracker. Owned hosts are intentionally absent from the global host
// list and must retain their tracker-scoped upload target.
func appendOwnedPolicyUploadHosts(candidates []string, tracker string, policy imageHostPolicy) []string {
	for _, host := range policy.uploadHosts {
		if owner := trackerForOwnedHost(host); owner != "" && strings.EqualFold(owner, tracker) {
			candidates = appendUniqueHost(candidates, host)
		}
	}
	return candidates
}

func configuredImageUploadHosts(appCfg config.Config) []string {
	cfg := appCfg.ImageHosting
	return normalizeConfiguredImageUploadHosts(cfg.Host1, cfg.Host2, cfg.Host3, cfg.Host4, cfg.Host5, cfg.Host6)
}

func conditionalImageHost(registry *Registry, appCfg config.Config, tracker string, trackerCfg config.TrackerConfig) (string, bool) {
	declared, ok := registry.LookupImageHostPolicy(tracker)
	if !ok {
		if registry == nil && strings.EqualFold(strings.TrimSpace(tracker), imagehostpolicy.OwnerForHost("lostimg")) {
			return "lostimg", appCfg.ImageHosting.LostimgEnabled
		}
		if registry == nil && strings.EqualFold(strings.TrimSpace(tracker), imagehostpolicy.OwnerForHost("reelflix")) {
			return "reelflix", strings.EqualFold(strings.TrimSpace(trackerCfg.ImageHost), "reelflix")
		}
		return "", false
	}
	host := strings.ToLower(strings.TrimSpace(declared.ConditionalHost))
	if host == "" {
		return "", false
	}
	if declared.EnableWithLostimg && appCfg.ImageHosting.LostimgEnabled {
		return host, true
	}
	if declared.EnableWhenConfigured && strings.EqualFold(strings.TrimSpace(trackerCfg.ImageHost), host) {
		return host, true
	}
	return host, false
}

func normalizeConfiguredImageUploadHosts(hosts ...string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" || !supportedUploadImageHost(normalized) {
			continue
		}
		if trackerForOwnedHost(normalized) != "" {
			continue
		}
		out = appendUniqueHost(out, normalized)
	}
	return out
}

func imageHostUsableForPolicy(tracker string, host string, policy imageHostPolicy) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || !supportedUploadImageHost(host) {
		return false
	}
	if owner := trackerForOwnedHost(host); owner != "" && !strings.EqualFold(owner, tracker) {
		return false
	}
	return len(policy.allowed) == 0 || hostAllowed(host, policy.allowed)
}

func trackerConfigForImageHostPolicy(appCfg config.Config, tracker string) config.TrackerConfig {
	if len(appCfg.Trackers.Trackers) == 0 {
		return config.TrackerConfig{}
	}
	if cfg, ok := appCfg.Trackers.Trackers[tracker]; ok {
		return cfg
	}
	for name, cfg := range appCfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), tracker) {
			return cfg
		}
	}
	return config.TrackerConfig{}
}

func supportedUploadImageHost(host string) bool {
	return imagehostpolicy.IsUploadHost(host)
}

func newImageHostPolicy(hosts ...string) imageHostPolicy {
	normalized := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		trimmed := strings.ToLower(strings.TrimSpace(host))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return imageHostPolicy{
		allowed:     normalized,
		uploadHosts: uploadHostsFor(normalized),
		preferred:   uploadHostsFor(normalized),
		required:    true,
	}
}

func newPreferredImageHostPolicy(host string, fallbackHosts ...string) imageHostPolicy {
	hosts := make([]string, 0, len(fallbackHosts)+1)
	if supportedUploadImageHost(host) {
		hosts = appendUniqueHost(hosts, host)
	}
	for _, fallbackHost := range fallbackHosts {
		if supportedUploadImageHost(fallbackHost) {
			hosts = appendUniqueHost(hosts, fallbackHost)
		}
	}
	return imageHostPolicy{
		uploadHosts: hosts,
		preferred:   hosts,
		required:    len(hosts) > 0,
		fallbackOK:  len(hosts) > 0,
	}
}

func withUnrestrictedImageHostFallbacks(registry *Registry, tracker string, policy imageHostPolicy, appCfg config.Config) imageHostPolicy {
	if !policy.required || len(policy.allowed) > 0 || !policy.fallbackOK {
		return policy
	}
	for _, host := range imageUploadCandidatesForTracker(registry, appCfg, tracker, configuredImageUploadHosts(appCfg)) {
		if !imageHostUsableForPolicy(tracker, host, policy) {
			continue
		}
		policy.uploadHosts = appendUniqueHost(policy.uploadHosts, host)
		policy.preferred = appendUniqueHost(policy.preferred, host)
	}
	return policy
}

func policyFromShared(policy imagehostpolicy.Policy) imageHostPolicy {
	return imageHostPolicy{
		allowed:     append([]string(nil), policy.AllowedHosts...),
		uploadHosts: append([]string(nil), policy.UploadHosts...),
		preferred:   append([]string(nil), policy.PreferredHosts...),
		required:    policy.Required,
	}
}

func uploadHostsFor(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if supportedUploadImageHost(host) {
			out = append(out, strings.ToLower(strings.TrimSpace(host)))
		}
	}
	return out
}

func prependHost(host string, hosts []string) []string {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" {
		return hosts
	}
	preferred := []string{normalized}
	for _, existing := range hosts {
		if strings.EqualFold(existing, normalized) {
			continue
		}
		preferred = append(preferred, existing)
	}
	return preferred
}

func appendUniqueTracker(trackers []string, tracker string) []string {
	if tracker == "" {
		return trackers
	}
	if slices.Contains(trackers, tracker) {
		return trackers
	}
	return append(trackers, tracker)
}

func appendUniqueHost(hosts []string, host string) []string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return hosts
	}
	if slices.Contains(hosts, host) {
		return hosts
	}
	return append(hosts, host)
}

func hostInList(host string, hosts []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	return slices.Contains(hosts, host)
}
