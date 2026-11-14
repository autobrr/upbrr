// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"math"
	"slices"
)

// CalibrationCenters chooses three separated informative regions from reference
// content alone. Target fits cannot select or tune their held-out region.
func CalibrationCenters(reference []FrameSample) ([]float64, string) {
	if len(reference) < 4 {
		return nil, "invalid_reference"
	}
	frames := make([]matchingFrame, len(reference))
	for i, frame := range reference {
		if !matchingTime(frame.TimestampSeconds) || len(frame.Luma) != len(reference[0].Luma) || len(frame.Luma) == 0 ||
			i > 0 && frame.TimestampSeconds <= reference[i-1].TimestampSeconds {
			return nil, "invalid_reference"
		}
		frames[i] = matchingFrame{frame: frame, signature: matchingSignature(frame.Luma)}
	}
	type transition struct{ time, change float64 }
	var candidates []transition
	first, last := reference[0].TimestampSeconds, reference[len(reference)-1].TimestampSeconds
	for i := 1; i < len(reference); i++ {
		point := frames[i]
		if point.frame.TimestampSeconds < first+4 || point.frame.TimestampSeconds > last-4 {
			continue
		}
		change := 1 - visualScore(frames[i-1], point)
		if len(point.signature) > 0 && len(frames[i-1].signature) > 0 && change > .03 {
			candidates = append(candidates, transition{point.frame.TimestampSeconds, change})
		}
	}
	slices.SortStableFunc(candidates, func(a, b transition) int { return cmp.Compare(b.change, a.change) })
	var centers []float64
	for _, candidate := range candidates {
		if slices.ContainsFunc(centers, func(center float64) bool { return math.Abs(center-candidate.time) < 12 }) {
			continue
		}
		centers = append(centers, candidate.time)
		if len(centers) == 3 {
			break
		}
	}
	if len(centers) != 3 {
		return nil, "uninformative_reference"
	}
	slices.Sort(centers)
	return centers, ""
}
