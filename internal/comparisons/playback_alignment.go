// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"bytes"
	"cmp"
	"math"
	"slices"
)

// PlaybackScale is one finite rational speed hypothesis, not a fitted warp.
type PlaybackScale struct{ Numerator, Denominator int64 }

// PlaybackScales proposes scale one plus the observed main/source rate ratio.
// Native timestamps/content must independently establish every proposal.
func PlaybackScales(mainNumerator, mainDenominator, sourceNumerator, sourceDenominator int64) []PlaybackScale {
	result := []PlaybackScale{{1, 1}}
	if mainNumerator <= 0 || mainDenominator <= 0 || sourceNumerator <= 0 || sourceDenominator <= 0 || mainNumerator > 1_000_000 ||
		sourceNumerator > 1_000_000 ||
		mainDenominator > 100000 ||
		sourceDenominator > 100000 {
		return result
	}
	n, d := mainNumerator*sourceDenominator, mainDenominator*sourceNumerator
	a, b := n, d
	for b != 0 {
		a, b = b, a%b
	}
	n /= a
	d /= a
	if n != d && float64(n)/float64(d) >= .5 && float64(n)/float64(d) <= 2 {
		result = append(result, PlaybackScale{n, d})
	}
	return result
}

// NativePlaybackScales adds exact standard-rate proposals established by native
// timestamps. Misleading nominal FPS cannot exclude the observed relationship.
// Proposals still require the same independent content/held-out proof.
func NativePlaybackScales(reference, target []FrameSample, declared []PlaybackScale) []PlaybackScale {
	type rate struct{ numerator, denominator int64 }
	standard := []rate{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1}, {60000, 1001}}
	observed := func(frames []FrameSample) []rate {
		if len(frames) < 4 {
			return nil
		}
		span := frames[len(frames)-1].TimestampSeconds - frames[0].TimestampSeconds
		if span < 1 {
			return nil
		}
		tick := 1e-9
		for _, frame := range frames {
			tick = max(tick, frame.TimebaseTickSeconds)
		}
		var result []rate
		for _, candidate := range standard {
			period := float64(candidate.denominator) / float64(candidate.numerator)
			if math.Abs(span-float64(len(frames)-1)*period) > 2*tick+1e-9 {
				continue
			}
			consistent := true
			for i := 1; i < len(frames); i++ {
				if math.Abs(frames[i].TimestampSeconds-frames[i-1].TimestampSeconds-period) > 2*tick+1e-9 {
					consistent = false
					break
				}
			}
			if consistent {
				result = append(result, candidate)
			}
		}
		return result
	}
	result := slices.Clone(declared)
	for _, main := range observed(reference) {
		for _, source := range observed(target) {
			for _, scale := range PlaybackScales(main.numerator, main.denominator, source.numerator, source.denominator) {
				if !slices.Contains(result, scale) {
					result = append(result, scale)
				}
				if len(result) > 9 {
					return nil
				}
			}
		}
	}
	return result
}

// DiscoverPlaybackAlignment fits only an offset for a fixed exact scale.
// One-to-one cadence proves point residuals; unequal cadence proves native
// display-interval overlap without deleting duplicate frame identities.
func DiscoverPlaybackAlignment(reference, target []FrameSample, scale PlaybackScale) (ConstantAlignment, string) {
	if scale.Numerator <= 0 || scale.Denominator <= 0 {
		return ConstantAlignment{}, "invalid_scale"
	}
	value := float64(scale.Numerator) / float64(scale.Denominator)
	if value < .5 || value > 2 {
		return ConstantAlignment{}, "invalid_scale"
	}
	transformed := slices.Clone(reference)
	for i := range transformed {
		transformed[i].TimestampSeconds *= value
		transformed[i].TimebaseTickSeconds *= value
	}
	alignment, reason := DiscoverAlignment(transformed, target)
	temporalRegionVerified := reason == ""
	if reason == "incompatible_cadence" {
		return discoverIntervalAlignment(transformed, target, scale)
	}
	if scale.Numerator == scale.Denominator && reason != "" {
		return alignment, reason
	}
	if reason != "" && reason != "no_visual_match" {
		return alignment, reason
	}
	columns, _, err := prepareMatching(
		[]SampledSource{{ID: "reference", Frames: transformed}, {ID: "target", Frames: target}},
		"reference",
		MatchOptions{Count: 1},
	)
	if err != nil {
		return ConstantAlignment{}, "invalid_samples"
	}
	var offsets, tolerances []float64
	var referenceFrameIDs []string
	previous := -1
	for index := 1; index < len(transformed)-1; index++ {
		point := columns[0].frames[index]
		before := columns[0].frames[index-1]
		// Weakly changing pictures cannot identify a subframe correspondence.
		// Register informative native transitions using both sides of the event.
		if visualScore(point, before) >= .97 {
			continue
		}
		strongest := true
		change := visualScore(point, before)
		for j := max(1, index-3); j <= min(len(transformed)-1, index+3); j++ {
			if j != index {
				other := visualScore(columns[0].frames[j], columns[0].frames[j-1])
				if other < change || other == change && j < index {
					strongest = false
					break
				}
			}
		}
		if !strongest {
			continue
		}
		best, bestScore := -1, 0.0
		for j, frame := range columns[1].frames {
			if j == 0 {
				continue
			}
			if temporalRegionVerified && math.Abs(frame.frame.TimestampSeconds-point.frame.TimestampSeconds-alignment.OffsetSeconds) > .25 {
				continue
			}
			score := min(visualScore(point, frame), visualScore(before, columns[1].frames[j-1]))
			if score > bestScore {
				best, bestScore = j, score
			}
		}
		if best < 0 || bestScore < .92 {
			return ConstantAlignment{}, "ambiguous_point_correspondence"
		}
		for j, frame := range columns[1].frames {
			if temporalRegionVerified {
				break
			}
			if j == 0 || math.Abs(frame.frame.TimestampSeconds-target[best].TimestampSeconds) <= .5 {
				continue
			}
			if min(visualScore(point, frame), visualScore(before, columns[1].frames[j-1])) >= bestScore-.03 {
				return ConstantAlignment{}, "ambiguous_point_correspondence"
			}
		}
		if best <= previous {
			return ConstantAlignment{}, "nonmonotone_correspondence"
		}
		previous = best
		step := min(
			transformed[index].TimestampSeconds-transformed[index-1].TimestampSeconds,
			transformed[index+1].TimestampSeconds-transformed[index].TimestampSeconds,
		)
		if best > 0 {
			step = min(step, target[best].TimestampSeconds-target[best-1].TimestampSeconds)
		}
		if best+1 < len(target) {
			step = min(step, target[best+1].TimestampSeconds-target[best].TimestampSeconds)
		}
		if step <= 0 || step > .25 {
			return ConstantAlignment{}, "unknown_display_interval"
		}
		offsets = append(offsets, target[best].TimestampSeconds-point.frame.TimestampSeconds)
		referenceFrameIDs = append(referenceFrameIDs, reference[index-1].FrameID, reference[index].FrameID)
		tolerances = append(tolerances, step/4+max(target[best].TimebaseTickSeconds, 1e-9))
	}
	if len(offsets) == 0 {
		return ConstantAlignment{}, "uninformative_reference"
	}
	sorted := slices.Clone(offsets)
	slices.Sort(sorted)
	offset := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		offset = (sorted[len(sorted)/2-1] + offset) / 2
	}
	tolerance := math.Inf(1)
	for i, residual := range offsets {
		if math.Abs(residual-offset) > tolerances[i] {
			return ConstantAlignment{}, "point_residual"
		}
		tolerance = min(tolerance, tolerances[i])
	}
	points := make([]AlignmentPoint, len(offsets))
	for i := range offsets {
		points[i] = AlignmentPoint{offsets[i], tolerances[i]}
	}
	return ConstantAlignment{
		Points:            points,
		ReferenceFrameIDs: referenceFrameIDs,
		OffsetSeconds:     offset,
		ToleranceSeconds:  tolerance,
		ScaleNumerator:    scale.Numerator,
		ScaleDenominator:  scale.Denominator,
	}, ""
}

// VerifyPlaybackCalibration fits only the first two regions. The third region
// validates that fixed fit and cannot select or adjust its offset.
func VerifyPlaybackCalibration(regions []ConstantAlignment) (ConstantAlignment, bool) {
	if len(regions) != 3 || regions[0].Scale() != regions[1].Scale() || regions[0].Scale() != regions[2].Scale() ||
		regions[0].IntervalProof != regions[1].IntervalProof ||
		regions[0].IntervalProof != regions[2].IntervalProof {
		return ConstantAlignment{}, false
	}
	fit := regions[0]
	fit.ToleranceSeconds = min(regions[0].ToleranceSeconds, regions[1].ToleranceSeconds)
	if fit.IntervalProof {
		lower := max(regions[0].OffsetSeconds-regions[0].ToleranceSeconds, regions[1].OffsetSeconds-regions[1].ToleranceSeconds)
		upper := min(regions[0].OffsetSeconds+regions[0].ToleranceSeconds, regions[1].OffsetSeconds+regions[1].ToleranceSeconds)
		if lower > upper {
			return ConstantAlignment{}, false
		}
		fit.OffsetSeconds, fit.ToleranceSeconds = (lower+upper)/2, max((upper-lower)/2, 1e-9)
	} else {
		var offsets []float64
		for _, region := range regions[:2] {
			for _, point := range region.Points {
				offsets = append(offsets, point.OffsetSeconds)
			}
		}
		if len(offsets) == 0 {
			return ConstantAlignment{}, false
		}
		slices.Sort(offsets)
		fit.OffsetSeconds = offsets[len(offsets)/2]
		if len(offsets)%2 == 0 {
			fit.OffsetSeconds = (fit.OffsetSeconds + offsets[len(offsets)/2-1]) / 2
		}
	}
	for _, region := range regions {
		if !region.validatesOffset(fit.OffsetSeconds) {
			return ConstantAlignment{}, false
		}
	}
	return fit, true
}

func discoverIntervalAlignment(reference, target []FrameSample, scale PlaybackScale) (ConstantAlignment, string) {
	columns, _, err := prepareMatching(
		[]SampledSource{{ID: "reference", Frames: reference}, {ID: "target", Frames: target}},
		"reference",
		MatchOptions{Count: 1},
	)
	if err != nil || len(reference) < 4 || len(target) < 4 {
		return ConstantAlignment{}, "invalid_samples"
	}
	if reference[len(reference)-1].TimestampSeconds-reference[0].TimestampSeconds < 1 ||
		visualScore(columns[0].frames[0], columns[0].frames[len(reference)-1]) >= .97 {
		return ConstantAlignment{}, "uninformative_reference"
	}
	type fit struct{ offset, lower, upper, score float64 }
	var fits []fit
	anchor := reference[len(reference)/2].TimestampSeconds
	for _, candidate := range target {
		offset := candidate.TimestampSeconds - anchor
		lower, upper, total := math.Inf(-1), math.Inf(1), 0.0
		previous := -1
		count := 0
		for i := 0; i < len(reference)-1; i += max(1, len(reference)/128) {
			predicted := reference[i].TimestampSeconds + offset
			index, _ := slices.BinarySearchFunc(
				target,
				predicted,
				func(frame FrameSample, seconds float64) int { return cmp.Compare(frame.TimestampSeconds, seconds) },
			)
			if index == len(target) || index > 0 && target[index].TimestampSeconds > predicted {
				index--
			}
			if index < 0 || index+1 >= len(target) || index < previous {
				count = 0
				break
			}
			score := visualScore(columns[0].frames[i], columns[1].frames[index])
			if score < .92 {
				count = 0
				break
			}
			left, right := index, index+1
			for left > 0 && bytes.Equal(target[left-1].Luma, target[index].Luma) {
				left--
			}
			for right < len(target)-1 && bytes.Equal(target[right].Luma, target[index].Luma) {
				right++
			}
			refEnd := reference[i+1].TimestampSeconds
			if refEnd-reference[i].TimestampSeconds > .25 || target[right].TimestampSeconds-target[left].TimestampSeconds > 1 {
				count = 0
				break
			}
			slack := max(target[index].TimebaseTickSeconds, 1e-9)
			lower = max(lower, target[left].TimestampSeconds-refEnd-slack)
			upper = min(upper, target[right].TimestampSeconds-reference[i].TimestampSeconds+slack)
			previous = index
			count++
			total += score
		}
		if count >= 4 && lower <= upper && !math.IsInf(lower, 0) && !math.IsInf(upper, 0) {
			fits = append(fits, fit{offset, lower, upper, total / float64(count)})
		}
	}
	if len(fits) == 0 {
		return ConstantAlignment{}, "no_interval_correspondence"
	}
	slices.SortStableFunc(fits, func(a, b fit) int { return cmp.Compare(b.score, a.score) })
	best := fits[0]
	for _, other := range fits[1:] {
		if math.Abs(other.offset-best.offset) > .5 && other.score >= best.score-.03 {
			return ConstantAlignment{}, "ambiguous_alignment"
		}
	}
	if best.upper-best.lower > .25 {
		return ConstantAlignment{}, "ambiguous_display_interval"
	}
	return ConstantAlignment{
		ScaleNumerator:   scale.Numerator,
		ScaleDenominator: scale.Denominator,
		OffsetSeconds:    (best.lower + best.upper) / 2,
		ToleranceSeconds: max((best.upper-best.lower)/2, 1e-9),
		IntervalProof:    true,
	}, ""
}
