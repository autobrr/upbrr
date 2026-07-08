// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const skipNotePrefix = "skip: "

func normalizeTracker(tracker string) string {
	return strings.ToUpper(strings.TrimSpace(tracker))
}

func handlerNotImplementedReason(tracker string) string {
	return "dupe search not implemented for tracker " + normalizeTracker(tracker)
}

func skipReason(meta api.PreparedMetadata, tracker string) (string, []string) {
	if len(meta.TrackerRuleFailures) == 0 {
		return "", nil
	}
	failures := meta.TrackerRuleFailures[tracker]
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

func noteSkip(reason string) string {
	return skipNotePrefix + strings.TrimSpace(reason)
}

func parseSkipReason(notes []string) (string, bool) {
	for _, note := range notes {
		trimmed := strings.TrimSpace(note)
		if !strings.HasPrefix(strings.ToLower(trimmed), skipNotePrefix) {
			continue
		}
		reason := strings.TrimSpace(trimmed[len(skipNotePrefix):])
		if reason != "" {
			return reason, true
		}
	}
	return "", false
}

func stringsToDupeEntries(values []string) []api.DupeEntry {
	if len(values) == 0 {
		return nil
	}
	entries := make([]api.DupeEntry, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		entries = append(entries, api.DupeEntry{Name: trimmed})
	}
	return entries
}

func trackerCfg(cfg config.Config, name string) (config.TrackerConfig, bool) {
	key := normalizeTracker(name)
	if key == "" || len(cfg.Trackers.Trackers) == 0 {
		return config.TrackerConfig{}, false
	}
	if entry, ok := cfg.Trackers.Trackers[key]; ok {
		return entry, true
	}
	for candidate, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(candidate, key) {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}

func trackerCfgWithAPIKey(cfg config.Config, name string) (config.TrackerConfig, string, bool) {
	tracker, ok := trackerCfg(cfg, name)
	if !ok {
		return config.TrackerConfig{}, "", false
	}
	apiKey := strings.TrimSpace(tracker.APIKey)
	if apiKey == "" {
		return config.TrackerConfig{}, "", false
	}
	return tracker, apiKey, true
}

func trackerCfgWithPasskey(cfg config.Config, name string) (config.TrackerConfig, string, bool) {
	tracker, ok := trackerCfg(cfg, name)
	if !ok {
		return config.TrackerConfig{}, "", false
	}
	passkey := strings.TrimSpace(tracker.Passkey)
	if passkey == "" {
		return config.TrackerConfig{}, "", false
	}
	return tracker, passkey, true
}
