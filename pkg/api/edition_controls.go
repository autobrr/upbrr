// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"
	"strings"
)

// NormalizeReleasePresentation accepts only supported complete presentation labels.
// The empty label explicitly clears detection.
func NormalizeReleasePresentation(value string) (string, bool) {
	switch strings.ToLower(strings.Join(strings.Fields(value), " ")) {
	case "":
		return "", true
	case "imax":
		return "IMAX", true
	case "open matte":
		return "Open Matte", true
	case "imax open matte":
		return "IMAX Open Matte", true
	default:
		return "", false
	}
}

// ReleaseFeatureLabel supplies the shared correction and manual naming vocabulary.
// Unknown values return empty so callers cannot silently accept unsupported facts.
func ReleaseFeatureLabel(feature ReleaseFeature) string {
	switch feature {
	case ReleaseFeatureHybrid:
		return "Hybrid"
	case ReleaseFeatureRestoration:
		return "Restored"
	case ReleaseFeatureRemaster:
		return "Remastered"
	case ReleaseFeatureTwoDiscSet:
		return "2-Disc Set"
	case ReleaseFeature4KRestoration:
		return "4K Restoration"
	case ReleaseFeature4KRemaster:
		return "4K Remaster"
	case ReleaseFeatureExtras:
		return "Extras"
	case ReleaseFeature2D3DEdition:
		return "2D/3D Edition"
	case ReleaseFeature3DAnaglyph:
		return "3D Anaglyph"
	case ReleaseFeature3DFullSBS:
		return "3D Full SBS"
	case ReleaseFeature3DHalfOU:
		return "3D Half OU"
	case ReleaseFeature3DHalfSBS:
		return "3D Half SBS"
	default:
		return ""
	}
}

// SeparateEditionOverride converts only fully recognized manual compounds into
// independent corrections. Unknown wording is returned untouched. A plain cut
// stays in Edition; no substring can establish an independent feature.
func SeparateEditionOverride(value string) ReleaseNameOverrides {
	value = strings.TrimSpace(value)
	result := ReleaseNameOverrides{Edition: new(value)}
	var editions, presentations []string
	var features []ReleaseFeature
	for part := range strings.SplitSeq(value, " / ") {
		part = strings.TrimSpace(part)
		if presentation, ok := NormalizeReleasePresentation(part); ok && presentation != "" {
			if !slices.Contains(presentations, presentation) {
				presentations = append(presentations, presentation)
			}
			continue
		}
		normalized := strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer("’", "'", ".", " ", "_", " ", "-", " ").Replace(part)), " "))
		var feature ReleaseFeature
		switch normalized {
		case "hybrid":
			feature = ReleaseFeatureHybrid
		case "restored", "restored edition", "restoration", "restored version":
			feature = ReleaseFeatureRestoration
		case "remaster", "remastered", "remastered edition", "remastered version":
			feature = ReleaseFeatureRemaster
		case "2 disc set":
			feature = ReleaseFeatureTwoDiscSet
		case "4k restoration":
			feature = ReleaseFeature4KRestoration
		case "4k remaster", "4k remastered":
			feature = ReleaseFeature4KRemaster
		case "extras":
			feature = ReleaseFeatureExtras
		case "2d/3d edition", "2d/3d", "2d 3d edition", "2d 3d":
			feature = ReleaseFeature2D3DEdition
		case "3d anaglyph", "anaglyph":
			feature = ReleaseFeature3DAnaglyph
		case "3d full sbs", "full sbs", "fsbs":
			feature = ReleaseFeature3DFullSBS
		case "3d half ou", "half ou", "hou":
			feature = ReleaseFeature3DHalfOU
		case "3d half sbs", "half sbs", "hsbs":
			feature = ReleaseFeature3DHalfSBS
		case "director's cut",
			"directors cut",
			"extended",
			"extended cut",
			"extended edition",
			"uncut",
			"unrated",
			"theatrical",
			"theatrical cut",
			"theatrical edition",
			"theatrical version",
			"final cut":
			editions = append(editions, part)
		default:
			return result
		}
		if feature != "" && !slices.Contains(features, feature) {
			features = append(features, feature)
		}
	}
	if len(features) == 0 && len(presentations) == 0 {
		return result
	}
	result.Edition = new(strings.Join(editions, " / "))
	if len(features) > 0 {
		result.ReleaseFeatures = &features
	}
	if len(presentations) > 0 {
		presentation := strings.Join(presentations, " ")
		if strings.Contains(presentation, "IMAX") && strings.Contains(presentation, "Open Matte") {
			presentation = "IMAX Open Matte"
		}
		result.Presentation = new(presentation)
	}
	return result
}
