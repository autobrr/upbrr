// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/rls"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

var releaseFeaturePatterns = []struct {
	label   string
	feature api.ReleaseFeature
	pattern *regexp.Regexp
}{
	{"Rifftrax", "", regexp.MustCompile(`(?i)\brifftrax\b`)},
	{"2in1", "", regexp.MustCompile(`(?i)\b2in1\b`)},
	{"2-Disc Set", api.ReleaseFeatureTwoDiscSet, regexp.MustCompile(`(?i)\b2[ ._-]*disc[ ._-]+set\b`)},
	{"4K Restoration", api.ReleaseFeature4KRestoration, regexp.MustCompile(`(?i)\b4k[ ._-]+restoration\b`)},
	{"4K Remaster", api.ReleaseFeature4KRemaster, regexp.MustCompile(`(?i)\b4k[ ._-]+remaster(?:ed)?\b`)},
	{"Extras", api.ReleaseFeatureExtras, regexp.MustCompile(`(?i)\bextras\b`)},
	{"2D/3D Edition", api.ReleaseFeature2D3DEdition, regexp.MustCompile(`(?i)\b2d[ ./_-]+3d(?:[ ._-]+edition)?\b`)},
	{"3D Anaglyph", api.ReleaseFeature3DAnaglyph, regexp.MustCompile(`(?i)\banaglyph\b`)},
	{"3D Full SBS", api.ReleaseFeature3DFullSBS, regexp.MustCompile(`(?i)\bf(?:ull)?[ ._-]*sbs\b`)},
	{"3D Half OU", api.ReleaseFeature3DHalfOU, regexp.MustCompile(`(?i)\bh(?:alf)?[ ._-]*ou\b`)},
	{"3D Half SBS", api.ReleaseFeature3DHalfSBS, regexp.MustCompile(`(?i)\bh(?:alf)?[ ._-]*sbs\b`)},
}

// parsedReleaseFeatures preserves explicit feature tokens from the original
// technical suffix. Title and group text cannot supply feature evidence.
func parsedReleaseFeatures(release rls.Release) []string {
	var suffix strings.Builder
	technical := false
	for _, tag := range release.Tags() {
		if tag.Is(rls.TagTypeGroup, rls.TagTypeExt) {
			break
		}
		if tag.Is(rls.TagTypeSource, rls.TagTypeResolution) || release.Type == rls.Movie && tag.Is(rls.TagTypeDate) {
			technical = true
		}
		if technical {
			fmt.Fprintf(&suffix, "%o", tag)
		}
	}
	var features []string
	text := strings.ReplaceAll(suffix.String(), "_", " ")
	for _, feature := range releaseFeaturePatterns {
		if feature.pattern.MatchString(text) {
			features = append(features, feature.label)
		}
	}
	return features
}

// applyReleaseFeatures separates edition identity from source-backed feature
// annotations before tracker mapping. Manual edition controls replace both.
func applyReleaseFeatures(parts *releaseEditionParts, meta preparationstate.State) {
	for _, marker := range meta.Release.Other {
		for _, feature := range releaseFeaturePatterns {
			if marker != feature.label {
				continue
			}
			switch {
			case feature.feature != "":
				parts.Features = append(parts.Features, feature.feature)
			case feature.label == "2in1":
				if parts.Set == "" {
					parts.Set = feature.label
				}
			default:
				parts.Edition = appendEditionLabel(parts.Edition, feature.label)
			}
		}
	}
}

func appendEditionLabel(value, label string) string {
	if slices.Contains(strings.Split(value, " / "), label) {
		return value
	}
	if value == "" {
		return label
	}
	return value + " / " + label
}

// ignoreSolitaryTheatrical removes the ordinary cut only when no other edition
// identity accompanies it. Presentations and features do not establish another
// edition; genuine edition compounds and selected sets remain intact.
func ignoreSolitaryTheatrical(parts releaseEditionParts) releaseEditionParts {
	if parts.Set != "" {
		return parts
	}
	for _, value := range []string{parts.Cut, parts.Edition} {
		if value != "" && !isTheatricalLabel(value) {
			return parts
		}
	}
	parts.Cut, parts.Edition = "", ""
	return parts
}

func isTheatricalLabel(value string) bool {
	for part := range strings.SplitSeq(value, "/") {
		part = strings.NewReplacer(".", " ", "-", " ", "_", " ").Replace(part)
		part = strings.ToLower(strings.Join(strings.Fields(part), " "))
		if part != "theatrical" && part != "theatrical cut" && part != "theatrical edition" && part != "theatrical version" {
			return false
		}
	}
	return true
}
