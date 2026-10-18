// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// editionFeature classifies complete, already separated parser/provider labels.
// It never searches arbitrary wording for a feature substring, and generic
// mastering labels do not imply a 4K source.
func editionFeature(value string) api.ReleaseFeature {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, feature := range releaseFeaturePatterns {
		if feature.feature != "" && (strings.EqualFold(value, feature.label) || feature.pattern.FindString(value) == value) {
			return feature.feature
		}
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(value)), " "))
	switch normalized {
	case "hybrid":
		return api.ReleaseFeatureHybrid
	case "restored", "restored edition", "restoration", "restored version":
		return api.ReleaseFeatureRestoration
	case "remastered", "remastered edition", "remaster", "remastered version":
		return api.ReleaseFeatureRemaster
	default:
		return ""
	}
}

// parserEditionDisplay retains the existing parser cleanup result. If cleanup
// collapses several labels (for example to Extended), the display projection
// preserves that result without inventing feature-label provenance.
func parserEditionDisplay(values []string) []api.EditionDisplayPart {
	text := cleanEditionText(removeHybrid(strings.Join(values, " ")))
	if text == "" {
		return nil
	}
	var parts []api.EditionDisplayPart
	for _, value := range values {
		if label := cleanEditionText(removeHybrid(value)); label != "" {
			parts = append(parts, api.EditionDisplayPart{Text: label, Feature: editionFeature(value)})
		}
	}
	if api.EditionDisplayText(parts) != text {
		return []api.EditionDisplayPart{{Text: text}}
	}
	return parts
}
