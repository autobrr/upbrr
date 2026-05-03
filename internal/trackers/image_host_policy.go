// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type imageHostPolicy struct {
	allowed   []string
	preferred []string
	required  bool
}

type ImageUploadTarget struct {
	Host       string
	UsageScope string
}

func policyForTracker(tracker string, trackerCfg config.TrackerConfig) imageHostPolicy {
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "A4K":
		return newImageHostPolicy(true, "ptpimg", "onlyimage", "imgbox", "ptscreens", "imgbb", "imgur", "postimg")
	case "BHD":
		return newImageHostPolicy(true, "ptpimg", "imgbox", "imgbb", "pixhost", "bhd", "bam")
	case "DC":
		return newImageHostPolicy(true, "imgbox", "imgbb", "bhd", "imgur", "postimg", "sharex")
	case "GPW":
		return newImageHostPolicy(true, "kshare", "pixhost", "ptpimg", "pterclub", "ilikeshots", "imgbox")
	case "HDB":
		if trackerCfg.ImgRehost {
			return newImageHostPolicy(true, "hdb")
		}
		return imageHostPolicy{}
	case "MTV":
		return newImageHostPolicy(true, "ptpimg", "imgbox", "imgbb")
	case "OE":
		return newImageHostPolicy(true, "ptpimg", "imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage")
	case "PTP":
		return newImageHostPolicy(true, "ptpimg", "pixhost")
	case "STC":
		return newImageHostPolicy(true, "imgbox", "imgbb")
	case "TVC":
		return newImageHostPolicy(true, "imgbb", "ptpimg", "imgbox", "pixhost", "bam", "onlyimage")
	default:
		return imageHostPolicy{}
	}
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
	if len(policy.allowed) == 0 {
		return newImageHostPolicy(true, host), nil
	}
	for _, allowed := range policy.allowed {
		if allowed != host {
			continue
		}
		preferred := []string{host}
		for _, existing := range policy.preferred {
			if existing == host {
				continue
			}
			preferred = append(preferred, existing)
		}
		policy.preferred = preferred
		return policy, nil
	}
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
	return forceImageHostPolicy(policy, host), nil
}

func RequiredImageUploadTargets(appCfg config.Config, trackerNames []string, overrides api.ImageHostOverrides) ([]ImageUploadTarget, error) {
	targets := make([]ImageUploadTarget, 0, len(trackerNames))
	seen := make(map[string]struct{}, len(trackerNames))
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
		scope := usageScopeForHost(name, host)
		key := host + "\x00" + scope
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, ImageUploadTarget{
			Host:       host,
			UsageScope: scope,
		})
	}
	return targets, nil
}

func ConfiguredImageUploadTargets(appCfg config.Config, trackerNames []string) ([]ImageUploadTarget, error) {
	targets := make([]ImageUploadTarget, 0, len(trackerNames))
	seen := make(map[string]struct{}, len(trackerNames))
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
		scope := usageScopeForHost(name, host)
		key := host + "\x00" + scope
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, ImageUploadTarget{
			Host:       host,
			UsageScope: scope,
		})
	}
	return targets, nil
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

func forceImageHostPolicy(policy imageHostPolicy, host string) imageHostPolicy {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" {
		return policy
	}
	return imageHostPolicy{
		allowed:   []string{normalized},
		preferred: []string{normalized},
		required:  true,
	}
}

func supportedUploadImageHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "dalexni", "hdb", "imgbb", "imgbox", "lensdump", "onlyimage", "passtheimage", "pixhost", "ptpimg", "ptscreens", "seedpool_cdn", "sharex", "utppm", "zipline":
		return true
	default:
		return false
	}
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
		allowed:   normalized,
		preferred: append([]string{}, normalized...),
		required:  required,
	}
}
