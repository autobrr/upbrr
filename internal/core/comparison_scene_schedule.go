// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"math"

	"github.com/autobrr/upbrr/internal/services/screenshots"
)

// Retarget after each verified row, counting overlapping qualifications. Failed
// searches and Quick discovery expansions never reset a category's allowance.
func comparisonSceneCategory(targets, achieved, attempts [3]int) int {
	category, bestUnmet, bestTarget := -1, 0, 1
	for c, target := range targets {
		if achieved[c] >= target || attempts[c] >= 10*target {
			continue
		}
		unmet := target - achieved[c]
		if category < 0 || unmet*bestTarget > bestUnmet*target {
			category, bestUnmet, bestTarget = c, unmet, target
		}
	}
	return category
}

// A local all-intra exception requires real, continuous display coverage around
// this row. Request duration, repeated reads and a calibrated offset prove none.
func comparisonIntraOnlyRegion(frames []screenshots.ComparisonSample, center, duration float64) bool {
	minimumFrames, minimumSeconds := 32, 8.0
	wholeSource := duration > 0 && duration <= minimumSeconds
	if wholeSource {
		minimumFrames, minimumSeconds = 4, duration
	}
	if len(frames) < minimumFrames {
		return false
	}
	start, end := math.Inf(1), math.Inf(-1)
	seen := map[string]bool{}
	fingerprint := ""
	quantum := 0.0
	firstTolerance, lastTolerance := 0.0, 0.0
	var previous screenshots.ComparisonSample
	for _, frame := range frames {
		if !frame.NativeSequence || frame.FrameID == "" || frame.PictureType != "I" || frame.Identity == nil || frame.Replay == nil ||
			frame.Replay.Identity != *frame.Identity ||
			frame.DurationTicks <= 0 ||
			frame.TimeBaseNumerator <= 0 ||
			frame.TimeBaseDenominator <= 0 {
			return false
		}
		if fingerprint == "" {
			fingerprint = frame.Identity.InputFingerprint
		}
		if frame.Identity.InputFingerprint != fingerprint {
			return false
		}
		if seen[frame.FrameID] {
			continue
		}
		seen[frame.FrameID] = true
		tick := float64(frame.TimeBaseNumerator) / float64(frame.TimeBaseDenominator)
		quantum = max(quantum, tick)
		lastTolerance = 0
		if frame.DurationTicks > 1 {
			lastTolerance = tick
		}
		if math.IsInf(start, 1) {
			firstTolerance = lastTolerance
		}
		t := frame.RelativeTimestampSeconds
		if !math.IsInf(end, -1) &&
			(!comparisonFileJoin(previous.ComparisonFrameObservation, frame.ComparisonFrameObservation) || t > end+1e-9 && (t > end+tick+1e-9 || !comparisonRoundedNativeBoundary(previous, frame))) {
			return false
		}
		previous = frame
		start = min(start, t)
		end = max(end, t+float64(frame.DurationTicks)*tick)
	}
	return len(seen) >= minimumFrames && end-start >= minimumSeconds-quantum-1e-9 && center >= start && center < end &&
		(!wholeSource || start <= firstTolerance+1e-9 && end >= duration-lastTolerance-1e-9)
}

func comparisonMatchingAllowance(rows, sources int) float64 {
	if rows <= 0 {
		rows = 4
	}
	return min(7200, max(900, float64(120+30*rows*sources)))
}

func comparisonAutomaticDecodeLimit(rows int) float64 {
	return float64(min(3600, 192+160*rows))
}
