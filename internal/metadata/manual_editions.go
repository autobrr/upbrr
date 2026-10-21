// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// manualEditionParts splits only complete supported labels. If any fragment is
// unknown, the original wording remains opaque, including its punctuation.
func manualEditionParts(value string) releaseEditionParts {
	separated := api.SeparateEditionOverride(value)
	parts := releaseEditionParts{Edition: *separated.Edition}
	if separated.Presentation != nil {
		parts.Presentation = *separated.Presentation
	}
	if separated.ReleaseFeatures != nil {
		parts.Features = slices.Clone(*separated.ReleaseFeatures)
	}
	if parts.Presentation != "" || len(parts.Features) > 0 {
		parts.Cut, parts.Edition = parts.Edition, ""
	}
	switch strings.ToLower(strings.ReplaceAll(parts.Edition, "’", "'")) {
	case "director's cut", "directors cut", "extended cut", "theatrical cut", "final cut":
		parts.Cut, parts.Edition = parts.Edition, ""
	}
	return ignoreSolitaryTheatrical(parts)
}

// applyManualEdition replaces editorial identity while retaining independent
// detected features/presentation. Recognized legacy compounds add their facts;
// explicit feature/presentation controls are applied afterward and take priority.
func applyManualEdition(meta *preparationstate.State, value string) {
	parts := manualEditionParts(value)
	meta.EditionSet, meta.Cut, meta.Edition = "", parts.Cut, parts.Edition
	var display []api.EditionDisplayPart
	if parts.Edition != "" {
		display = append(display, api.EditionDisplayPart{Text: parts.Edition})
	}
	for _, part := range meta.EditionDisplay {
		if part.Feature != "" {
			display = append(display, part)
		}
	}
	meta.EditionDisplay = display
	if parts.Presentation != "" {
		meta.Presentation = parts.Presentation
	}
	for _, feature := range parts.Features {
		if !slices.Contains(meta.ReleaseFeatures, feature) {
			meta.ReleaseFeatures = append(meta.ReleaseFeatures, feature)
		}
		if feature != api.ReleaseFeatureHybrid &&
			!slices.ContainsFunc(meta.EditionDisplay, func(part api.EditionDisplayPart) bool { return part.Feature == feature }) {
			meta.EditionDisplay = append(meta.EditionDisplay, api.EditionDisplayPart{Text: api.ReleaseFeatureLabel(feature), Feature: feature})
		}
	}
	if api.EditionDisplayText(meta.EditionDisplay) == meta.Edition {
		meta.EditionDisplay = nil
	}
	meta.Release.Cut, meta.Release.Edition = nil, nil
	if meta.Cut != "" {
		meta.Release.Cut = []string{meta.Cut}
	}
	if meta.Edition != "" {
		meta.Release.Edition = []string{meta.Edition}
	}
}

// applyManualReleaseFeatures replaces the semantic set and its display-only
// labels together. Hybrid keeps its existing independent naming role.
func applyManualReleaseFeatures(meta *preparationstate.State, features []api.ReleaseFeature) {
	meta.ReleaseFeatures = nil
	meta.EditionDisplay = nil
	if meta.Edition != "" && meta.EditionSet == "" {
		meta.EditionDisplay = append(meta.EditionDisplay, api.EditionDisplayPart{Text: meta.Edition})
	}
	for _, feature := range features {
		if slices.Contains(meta.ReleaseFeatures, feature) {
			continue
		}
		meta.ReleaseFeatures = append(meta.ReleaseFeatures, feature)
		if feature != api.ReleaseFeatureHybrid {
			meta.EditionDisplay = append(meta.EditionDisplay, api.EditionDisplayPart{Text: api.ReleaseFeatureLabel(feature), Feature: feature})
		}
	}
	if api.EditionDisplayText(meta.EditionDisplay) == meta.Edition {
		meta.EditionDisplay = nil
	}
}
