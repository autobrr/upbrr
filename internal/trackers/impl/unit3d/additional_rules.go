// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var ruleResolutionOrder = map[string]int{
	"480i":  1,
	"480p":  2,
	"576i":  3,
	"576p":  4,
	"720p":  5,
	"1080i": 6,
	"1080p": 7,
	"1440p": 8,
	"2160p": 9,
	"4320p": 10,
	"8640p": 11,
}

// ResolutionBelow reports whether value ranks below minimum in the Unit3D resolution order.
func ResolutionBelow(value, minimum string) bool {
	return ruleResolutionOrder[value] < ruleResolutionOrder[minimum]
}

// RuleType returns the normalized release type used by site-specific rules.
func RuleType(meta api.RuleSubject) string {
	value := strings.ToUpper(strings.TrimSpace(meta.Type))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return value
}

// RuleGroup returns the parsed release group or the normalized tag fallback.
func RuleGroup(meta api.RuleSubject) string {
	if group := strings.TrimSpace(meta.Release.Group); group != "" {
		return group
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
}

// DolbyVisionOnly reports whether the release has Dolby Vision without HDR fallback.
func DolbyVisionOnly(meta api.RuleSubject) bool {
	if meta.WebDV {
		return true
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	return strings.Contains(hdr, "DV") && !strings.Contains(hdr, "HDR")
}

// RuleGenres returns normalized current-source release and provider genres.
func RuleGenres(meta api.RuleSubject) []string {
	values := splitRuleList(meta.Release.Genre)
	if ruleMetadataMatchesSource(meta) && meta.ProviderMetadata.TMDB != nil {
		values = append(values, splitRuleList(meta.ProviderMetadata.TMDB.Genres)...)
	}
	if ruleMetadataMatchesSource(meta) && meta.ProviderMetadata.IMDB != nil {
		values = append(values, splitRuleList(meta.ProviderMetadata.IMDB.Genres)...)
	}
	return NormalizeRuleValues(values)
}

// RuleKeywords returns normalized current-source TMDB keywords.
func RuleKeywords(meta api.RuleSubject) []string {
	if !ruleMetadataMatchesSource(meta) || meta.ProviderMetadata.TMDB == nil {
		return nil
	}
	return NormalizeRuleValues(splitRuleList(meta.ProviderMetadata.TMDB.Keywords))
}

// AdultContent reports whether current genres or keywords contain an adult marker.
func AdultContent(meta api.RuleSubject) bool {
	for _, token := range append(RuleGenres(meta), RuleKeywords(meta)...) {
		switch token {
		case "adult", "porn", "pornography", "xxx", "erotic":
			return true
		}
	}
	return false
}

// Anime reports whether current metadata classifies the release as anime.
func Anime(meta api.RuleSubject) bool {
	if ruleMetadataMatchesSource(meta) && meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Anime {
		return true
	}
	return ContainsRuleValue(RuleKeywords(meta), []string{"anime"})
}

// Animation reports whether current metadata contains the animation genre.
func Animation(meta api.RuleSubject) bool {
	return ContainsRuleValue(RuleGenres(meta), []string{"animation"})
}

// NormalizeRuleValues trims, lowercases, and deduplicates values while preserving order.
func NormalizeRuleValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

// ContainsRuleValue reports whether normalized values and targets intersect.
func ContainsRuleValue(values, targets []string) bool {
	if len(values) == 0 || len(targets) == 0 {
		return false
	}
	targetSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		targetSet[strings.ToLower(strings.TrimSpace(target))] = true
	}
	for _, value := range values {
		if targetSet[strings.ToLower(strings.TrimSpace(value))] {
			return true
		}
	}
	return false
}

func ruleMetadataMatchesSource(meta api.RuleSubject) bool {
	storedSource := strings.TrimSpace(meta.ProviderMetadata.SourcePath)
	return storedSource == "" || strings.EqualFold(storedSource, strings.TrimSpace(meta.SourcePath))
}

func splitRuleList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
