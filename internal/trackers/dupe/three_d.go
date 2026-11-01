// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"strings"
	"unicode"
)

// ThreeDFromLabels extracts positive stereoscopic evidence from tracker labels.
// Missing labels and mixed 2D/3D editions do not establish a presentation.
func ThreeDFromLabels(labels ...string) string {
	for _, label := range labels {
		tokens := strings.FieldsFunc(strings.ToUpper(label), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if slices.Contains(tokens, "2D") {
			continue
		}
		for _, token := range tokens {
			switch token {
			case "3D", "BLURAY3D", "HSBS", "FSBS", "HOU", "FOU", "HTAB", "FTAB", "MVC":
				return "3d"
			}
		}
	}
	return ""
}

func canonicalThreeD(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "2D") {
		return "2d"
	}
	return ThreeDFromLabels(value)
}

func threeDFromTitle(metadata string) string {
	// A terminal hyphenated marker can be the release group, even when the
	// parser recognizes it as a technical token. Keep that ambiguity unknown.
	if separator := strings.LastIndex(metadata, "-"); separator >= 0 {
		suffix := metadata[separator+1:]
		if !strings.ContainsAny(suffix, " ._") && ThreeDFromLabels(suffix) != "" {
			metadata = metadata[:separator]
		}
	}
	return ThreeDFromLabels(metadata)
}

func candidateThreeDFact(candidate TrackerCandidate, title string) Fact {
	labels := append([]string{candidate.Type, candidate.Source}, candidate.Flags...)
	structured := mergeStructuredAndTitleFact(
		canonicalThreeD(candidate.ThreeD),
		"threeD",
		ThreeDFromLabels(labels...),
		FactOriginTrackerAPI,
		FactOriginTrackerAPI,
	)
	if structured.Status == FactContradictory {
		return structured
	}
	return mergeStructuredAndTitleFact(structured.Value, "threeD", title, FactOriginTrackerAPI, FactOriginTrackerTitle)
}
