// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"
	"strings"
)

// IsThreeDSubtype identifies existing stereoscopic subtype facts, not a mixed 2D/3D edition set.
func IsThreeDSubtype(feature ReleaseFeature) bool {
	switch feature {
	case ReleaseFeature3DAnaglyph, ReleaseFeature3DFullSBS, ReleaseFeature3DHalfOU, ReleaseFeature3DHalfSBS:
		return true
	case ReleaseFeatureHybrid, ReleaseFeatureRestoration, ReleaseFeatureRemaster, ReleaseFeatureTwoDiscSet,
		ReleaseFeature4KRestoration, ReleaseFeature4KRemaster, ReleaseFeatureExtras, ReleaseFeature2D3DEdition:
		return false
	default:
		return false
	}
}

// ThreeDName returns a single generic marker for measured 3D or a recognized subtype.
// Naming preserves subtype facts and does not establish measured 3D evidence.
func ThreeDName(detected string, features []ReleaseFeature) string {
	if strings.TrimSpace(detected) != "" || slices.ContainsFunc(features, IsThreeDSubtype) {
		return "3D"
	}
	return ""
}

// ThreeDName returns the subject's generic naming marker, honoring an explicit
// component omission without changing retained measured or subtype facts.
func (s UploadSubject) ThreeDName() string {
	if component, ok := s.GeneratedName.Component(NameRoleThreeD); ok && component.Manual && !component.Present {
		return ""
	}
	return ThreeDName(s.Is3D, s.ReleaseFeatures)
}

// EditionNamingText omits subtype fragments rendered by the independent 3D role.
// Other display consumers can still use the complete EditionDisplayText.
func EditionNamingText(parts []EditionDisplayPart) string {
	return EditionDisplayText(EditionDisplayWithoutThreeD(parts))
}

// EditionDisplayWithoutThreeD returns independent display parts without 3D
// subtypes or their orphaned separators, preserving the original evidence.
func EditionDisplayWithoutThreeD(parts []EditionDisplayPart) []EditionDisplayPart {
	filtered := make([]EditionDisplayPart, 0, len(parts))
	for _, part := range parts {
		if IsThreeDSubtype(part.Feature) {
			continue
		}
		if strings.TrimSpace(part.Text) == "/" && (len(filtered) == 0 || strings.TrimSpace(filtered[len(filtered)-1].Text) == "/") {
			continue
		}
		filtered = append(filtered, part)
	}
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1].Text) == "/" {
		filtered = filtered[:len(filtered)-1]
	}
	return filtered
}
