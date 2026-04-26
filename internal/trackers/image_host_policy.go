// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type imageHostPolicy struct {
	allowed   []string
	preferred []string
	required  bool
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
	case "THR":
		if strings.TrimSpace(trackerCfg.ImgAPI) != "" {
			return newImageHostPolicy(true, "thr")
		}
		return imageHostPolicy{}
	case "TVC":
		return newImageHostPolicy(true, "imgbb", "ptpimg", "imgbox", "pixhost", "bam", "onlyimage")
	default:
		return imageHostPolicy{}
	}
}

func applyImageHostOverrides(policy imageHostPolicy, overrides api.ImageHostOverrides) imageHostPolicy {
	if overrides.PreferredHost == nil {
		return policy
	}
	host := strings.ToLower(strings.TrimSpace(*overrides.PreferredHost))
	if host == "" {
		return policy
	}
	if len(policy.allowed) == 0 {
		policy.preferred = []string{host}
		return policy
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
		return policy
	}
	return policy
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
