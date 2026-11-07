// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"strings"
	"unicode"
)

// ThreeDFromLabels extracts positive stereoscopic evidence from tracker labels.
// Missing labels and mixed 2D/3D editions do not establish a presentation.
func ThreeDFromLabels(labels ...string) string {
	twoD, threeD := presentationFromLabels(labels...)
	if threeD && !twoD {
		return "3d"
	}
	return ""
}

func presentationFromLabels(labels ...string) (bool, bool) {
	twoD, threeD := false, false
	for _, label := range labels {
		tokens := strings.FieldsFunc(strings.ToUpper(label), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, token := range tokens {
			switch token {
			case "2D":
				twoD = true
			case "3D", "BLURAY3D", "HSBS", "FSBS", "HOU", "FOU", "HTAB", "FTAB", "MVC":
				threeD = true
			}
		}
	}
	return twoD, threeD
}

func canonicalThreeD(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "2D") {
		return "2d"
	}
	return ThreeDFromLabels(value)
}

func threeDFromTitle(metadata string, origin FactOrigin) Fact {
	// A terminal hyphenated marker can be the release group, even when the
	// parser recognizes it as a technical token. Keep that ambiguity unknown.
	if separator := strings.LastIndex(metadata, "-"); separator >= 0 {
		suffix := metadata[separator+1:]
		twoD, threeD := presentationFromLabels(suffix)
		if !strings.ContainsAny(suffix, " ._") && (twoD || threeD) {
			metadata = metadata[:separator]
		}
	}
	twoD, threeD := presentationFromLabels(metadata)
	if !twoD && !threeD {
		return missingFact()
	}
	fact := Fact{
		Status:       FactPartial,
		Origin:       origin,
		SourceFields: []string{"title"},
	}
	switch {
	case twoD && threeD:
		fact.Status = FactContradictory
		fact.Contradictions = []string{"2d", "3d"}
	case twoD:
		fact.Value = "2d"
	case threeD:
		fact.Value = "3d"
	}
	return fact
}

func candidateThreeDFact(candidate TrackerCandidate, title Fact) Fact {
	labels := append([]string{candidate.Type, candidate.Source}, candidate.Flags...)
	structured := mergeStructuredAndTitleFact(
		canonicalThreeD(candidate.ThreeD),
		"threeD",
		ThreeDFromLabels(labels...),
		FactOriginTrackerAPI,
		FactOriginTrackerAPI,
	)
	return mergeThreeDTitleFact(structured, title)
}

func mergeThreeDTitleFact(structured Fact, title Fact) Fact {
	if structured.Status == FactContradictory {
		return structured
	}
	if title.Status == FactContradictory {
		return title
	}
	return mergeStructuredAndTitleFact(structured.Value, "threeD", title.Value, structured.Origin, title.Origin)
}
