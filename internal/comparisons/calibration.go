// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"math"
	"slices"
)

// SelectCalibrationHypothesis coalesces numerically different rate hypotheses
// only when their predicted windows agree within the finer observed subframe
// tolerance over the entire main timeline. It chooses a search predictor, never
// a correspondence; every selected row still requires independent local proof.
func SelectCalibrationHypothesis(candidates []ConstantAlignment, duration float64) (ConstantAlignment, bool) {
	if len(candidates) == 0 || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return ConstantAlignment{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates {
		if !matchingUnit(candidate.ToleranceSeconds) || candidate.ToleranceSeconds == 0 || math.IsNaN(candidate.OffsetSeconds) ||
			math.IsInf(candidate.OffsetSeconds, 0) ||
			math.IsNaN(candidate.Scale()) ||
			math.IsInf(candidate.Scale(), 0) {
			return ConstantAlignment{}, false
		}
		if math.Abs(candidate.Scale()-1) < math.Abs(best.Scale()-1) {
			best = candidate
		}
	}
	for _, candidate := range candidates {
		for _, time := range []float64{0, duration} {
			if math.Abs(candidate.Predict(time)-best.Predict(time)) > min(candidate.ToleranceSeconds, best.ToleranceSeconds) {
				return ConstantAlignment{}, false
			}
		}
	}
	return best, true
}

// PlaybackSearchPrediction retains an observed middle-region predictor when
// three independently verified regions disagree slightly. This is only a search
// window: edits or telecine phase can change offsets locally, and every row must
// still establish its own native correspondence. The margin is the target
// window's spare radius after admitting the complete reference context.
func PlaybackSearchPrediction(regions []ConstantAlignment, margin float64) (ConstantAlignment, bool) {
	if len(regions) != 3 || margin <= 0 || math.IsNaN(margin) || math.IsInf(margin, 0) {
		return ConstantAlignment{}, false
	}
	prediction := regions[1]
	for _, region := range regions {
		if region.Scale() != prediction.Scale() || math.IsNaN(region.Scale()) || math.IsInf(region.Scale(), 0) || region.ToleranceSeconds <= 0 ||
			!matchingUnit(region.ToleranceSeconds) ||
			math.IsNaN(region.OffsetSeconds) ||
			math.IsInf(region.OffsetSeconds, 0) ||
			math.Abs(region.OffsetSeconds-prediction.OffsetSeconds) > margin {
			return ConstantAlignment{}, false
		}
	}
	prediction.ReferenceFrameIDs, prediction.Points = nil, nil
	return prediction, true
}

// CalibrationCenters chooses separated informative regions from reference
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
		frames[i] = matchingFrame{frame: frame, signature: frame.visualSignature()}
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
