// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imagehostpolicy"
	"github.com/autobrr/upbrr/pkg/api"
)

type imageHostPolicy struct {
	allowed     []string
	uploadHosts []string
	preferred   []string
	required    bool
}

type ImageUploadTarget struct {
	Host       string
	UsageScope string
	Trackers   []string
}

type imageUploadPolicyTarget struct {
	tracker string
	policy  imageHostPolicy
}

func policyForTracker(tracker string, trackerCfg config.TrackerConfig) imageHostPolicy {
	return policyFromShared(imagehostpolicy.ForTracker(tracker, trackerCfg.ImgRehost, trackerCfg.ImgAPI))
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
		return newImageHostPolicy(true, host), nil
	}
	if !hostAllowed(host, policy.allowed) {
		return imageHostPolicy{}, fmt.Errorf("trackers: %s image host override %q is not allowed (allowed: %s)", strings.TrimSpace(tracker), host, strings.Join(policy.allowed, ", "))
	}
	policy.preferred = prependHost(host, policy.preferred)
	return policy, nil
}

func resolveImageHostPolicy(tracker string, trackerCfg config.TrackerConfig, overrides api.ImageHostOverrides) (imageHostPolicy, error) {
	policy := policyForTracker(tracker, trackerCfg)
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
		return newImageHostPolicy(true, host), nil
	}
	policy.preferred = prependHost(host, policy.preferred)
	return policy, nil
}

func PreferredImageUploadHost(tracker string, trackerCfg config.TrackerConfig, overrides api.ImageHostOverrides) (string, error) {
	policy, err := resolveImageHostPolicy(tracker, trackerCfg, overrides)
	if err != nil {
		return "", err
	}
	return preferredHost(policy), nil
}

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

func NeededImageUploadTargets(appCfg config.Config, trackerNames []string, selectedHost string) ([]ImageUploadTarget, error) {
	return neededImageUploadTargets(appCfg, trackerNames, selectedHost, nil)
}

func NeededImageUploadTargetsExcluding(appCfg config.Config, trackerNames []string, selectedHost string, excludedHosts []string) ([]ImageUploadTarget, error) {
	excluded := make(map[string]struct{}, len(excludedHosts))
	for _, host := range excludedHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" {
			excluded[normalized] = struct{}{}
		}
	}
	return neededImageUploadTargets(appCfg, trackerNames, selectedHost, excluded)
}

func neededImageUploadTargets(appCfg config.Config, trackerNames []string, selectedHost string, excludedHosts map[string]struct{}) ([]ImageUploadTarget, error) {
	selectedHost = strings.ToLower(strings.TrimSpace(selectedHost))
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
			policy, err := resolveImageHostPolicy(name, trackerCfg, api.ImageHostOverrides{})
			if err != nil {
				return nil, err
			}
			if host := preferredHost(policy); host != "" {
				if _, excluded := excludedHosts[host]; !excluded {
					addTarget(host, name)
					continue
				}
			}
			flexibleTargets = append(flexibleTargets, imageUploadPolicyTarget{tracker: name, policy: policy})
			continue
		}

		policy := policyForTracker(name, trackerCfg)
		flexibleTargets = append(flexibleTargets, imageUploadPolicyTarget{tracker: name, policy: policy})
	}

	assignFlexibleImageUploadTargets(flexibleTargets, selectedHost, excludedHosts, targets, addTarget)

	if len(targets) == 0 && selectedHost != "" && trackerForOwnedHost(selectedHost) == "" {
		if _, excluded := excludedHosts[selectedHost]; excluded {
			return targets, nil
		}
		targets = append(targets, ImageUploadTarget{Host: selectedHost, UsageScope: globalImageUsageScope})
	}

	return targets, nil
}

func assignFlexibleImageUploadTargets(flexibleTargets []imageUploadPolicyTarget, selectedHost string, excludedHosts map[string]struct{}, targets []ImageUploadTarget, addTarget func(string, string)) {
	unassigned := make([]imageUploadPolicyTarget, 0, len(flexibleTargets))
	for _, target := range flexibleTargets {
		if host, ok := existingImageUploadTargetHost(target.tracker, target.policy, targets); ok {
			addTarget(host, target.tracker)
			continue
		}
		unassigned = append(unassigned, target)
	}

	for len(unassigned) > 0 {
		host := bestImageUploadTargetHost(unassigned, selectedHost, excludedHosts)
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

func existingImageUploadTargetHost(tracker string, policy imageHostPolicy, targets []ImageUploadTarget) (string, bool) {
	for _, target := range targets {
		if imageHostUsableForPolicy(tracker, target.Host, policy) {
			return target.Host, true
		}
	}
	return "", false
}

func bestImageUploadTargetHost(targets []imageUploadPolicyTarget, selectedHost string, excludedHosts map[string]struct{}) string {
	rankings := make(map[string]imageUploadHostRanking, len(targets))
	for _, target := range targets {
		for _, host := range candidateImageUploadTargetHosts(target.tracker, target.policy, selectedHost, excludedHosts) {
			ranking := rankings[host]
			ranking.host = host
			ranking.count++
			ranking.preference += preferredHostOrder(host, target.policy.preferred)
			if host == selectedHost {
				ranking.selected = true
			}
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
	selected   bool
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
	if candidate.selected != current.selected {
		return candidate.selected
	}
	if candidate.preference != current.preference {
		return candidate.preference < current.preference
	}
	return candidate.host < current.host
}

func candidateImageUploadTargetHosts(tracker string, policy imageHostPolicy, selectedHost string, excludedHosts map[string]struct{}) []string {
	hosts := make([]string, 0, len(policy.preferred)+1)
	if _, excluded := excludedHosts[selectedHost]; !excluded && imageHostUsableForPolicy(tracker, selectedHost, policy) {
		hosts = append(hosts, selectedHost)
	}
	for _, host := range policy.preferred {
		if _, excluded := excludedHosts[strings.ToLower(strings.TrimSpace(host))]; excluded {
			continue
		}
		if imageHostUsableForPolicy(tracker, host, policy) {
			hosts = appendUniqueHost(hosts, host)
		}
	}
	return hosts
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

func newImageHostPolicy(required bool, hosts ...string) imageHostPolicy {
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
		required:    required,
	}
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
	for _, existing := range trackers {
		if existing == tracker {
			return trackers
		}
	}
	return append(trackers, tracker)
}

func appendUniqueHost(hosts []string, host string) []string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return hosts
	}
	for _, existing := range hosts {
		if existing == host {
			return hosts
		}
	}
	return append(hosts, host)
}
