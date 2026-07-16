// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"sort"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func normalizeTracker(tracker string) string {
	return strings.ToUpper(strings.TrimSpace(tracker))
}

func skipReason(meta api.DuplicateSubject, tracker string) (string, []string) {
	if len(meta.TrackerRuleFailures) == 0 {
		return "", nil
	}
	failures := meta.TrackerRuleFailures[tracker]
	failures = api.BlockingRuleFailures(failures)
	if len(failures) == 0 {
		return "", nil
	}

	parts := make([]string, 0, len(failures))
	ruleSet := make(map[string]struct{}, len(failures))
	for _, failure := range failures {
		rule := strings.TrimSpace(failure.Rule)
		if rule != "" {
			ruleSet[rule] = struct{}{}
		}
		reason := strings.TrimSpace(failure.Reason)
		if reason == "" {
			reason = rule
		}
		if reason != "" {
			parts = append(parts, reason)
		}
	}

	rules := make([]string, 0, len(ruleSet))
	for rule := range ruleSet {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	if len(parts) == 0 {
		return "rule check failed", rules
	}
	return "rule check failed: " + strings.Join(parts, "; "), rules
}

func trimEntries(entries []api.DupeEntry) []api.DupeEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]api.DupeEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Name = strings.TrimSpace(entry.Name)
		entry.Link = strings.TrimSpace(entry.Link)
		entry.Download = strings.TrimSpace(entry.Download)
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Type = strings.TrimSpace(entry.Type)
		entry.Res = strings.TrimSpace(entry.Res)
		if entry.Name == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}
