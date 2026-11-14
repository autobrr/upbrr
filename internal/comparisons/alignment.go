// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"errors"
	"math"
	"slices"
)

// ConstantAlignment predicts source-relative time from independently checked
// temporal evidence. It is not an anchor, frame index or whole-timeline proof.
type ConstantAlignment struct {
	OffsetSeconds    float64
	ToleranceSeconds float64
}

// Consistent reports agreement within the finer observed subframe tolerance.
func (a ConstantAlignment) Consistent(b ConstantAlignment) bool {
	return math.Abs(a.OffsetSeconds-b.OffsetSeconds) <= min(a.ToleranceSeconds, b.ToleranceSeconds)
}

// DiscoverAlignment searches actual timestamps using a temporal sequence.
// An empty reason means an offset was established. Flat, repeated, variable or
// unequal-cadence evidence cannot establish one. Visual floors and margins match
// the ordinary matcher; every ordinary film frame contributes to the proof.
func DiscoverAlignment(reference, target []FrameSample) (ConstantAlignment, string) {
	columns, _, err := prepareMatching(
		[]SampledSource{{ID: "reference", Frames: reference}, {ID: "target", Frames: target}},
		"reference",
		MatchOptions{Count: 1},
	)
	if err != nil {
		return ConstantAlignment{}, "invalid_samples"
	}
	period := func(frames []FrameSample) float64 {
		if len(frames) < 4 {
			return 0
		}
		deltas := make([]float64, len(frames)-1)
		for i := range deltas {
			deltas[i] = frames[i+1].TimestampSeconds - frames[i].TimestampSeconds
		}
		slices.Sort(deltas)
		step := deltas[len(deltas)/2]
		if step < .008 || step > .25 || deltas[len(deltas)-1]-deltas[0] > max(.002, step*.01) {
			return 0
		}
		return step
	}
	step, targetStep := period(reference), period(target)
	if step == 0 || targetStep == 0 || math.Abs(step-targetStep) > max(.002, step*.02) {
		return ConstantAlignment{}, "incompatible_cadence"
	}
	var points []matchingFrame
	temporalChange := false
	// Bound high-frame-rate work while retaining every frame of a four-second
	// film-rate reference. Sparse points cannot precisely register a single cut.
	count := min(128, len(reference))
	for i := range count {
		frame := columns[0].frames[i*(len(reference)-1)/(count-1)]
		if len(frame.signature) == 0 {
			continue
		}
		// Texture alone cannot register time. Compare across the window so
		// gradual motion can provide evidence as well as a single cut.
		if len(points) > 0 && visualScore(points[0], frame) < .97 {
			temporalChange = true
		}
		points = append(points, frame)
	}
	if len(points) < 4 || !temporalChange || points[len(points)-1].frame.TimestampSeconds-points[0].frame.TimestampSeconds < 1 {
		return ConstantAlignment{}, "uninformative_reference"
	}
	scoreAt := func(offset float64) float64 {
		total := 0.0
		for _, point := range points {
			predicted := point.frame.TimestampSeconds + offset
			index, _ := slices.BinarySearchFunc(columns[1].frames, predicted, func(frame matchingFrame, seconds float64) int {
				return cmp.Compare(frame.frame.TimestampSeconds, seconds)
			})
			index = min(index, len(target)-1)
			if index > 0 && math.Abs(target[index-1].TimestampSeconds-predicted) < math.Abs(target[index].TimestampSeconds-predicted) {
				index--
			}
			if math.Abs(target[index].TimestampSeconds-predicted) > max(step, targetStep)/2+.001 {
				return 0
			}
			score := visualScore(point, columns[1].frames[index])
			if score < .92 {
				return 0
			}
			total += score
		}
		return total / float64(len(points))
	}
	anchor := points[len(points)/2].frame.TimestampSeconds
	scores := make([]float64, len(target))
	best, bestScore := 0.0, 0.0
	for i, frame := range target {
		offset := frame.TimestampSeconds - anchor
		scores[i] = scoreAt(offset)
		if scores[i] > bestScore {
			best, bestScore = offset, scores[i]
		}
	}
	if bestScore < .92 {
		return ConstantAlignment{}, "no_visual_match"
	}
	for i, frame := range target {
		if math.Abs(frame.TimestampSeconds-anchor-best) > .5 && scores[i] >= bestScore-.03 {
			return ConstantAlignment{}, "ambiguous_alignment"
		}
	}
	return ConstantAlignment{OffsetSeconds: best, ToleranceSeconds: min(step, targetStep) / 4}, ""
}

// AlignedMatch uses caller-verified constant offsets only for automatic default
// picture preferences without scene quotas. Manual/explicit choices keep Match.
// Target reuse is still rejected; static scenes cannot drift for a higher score.
func AlignedMatch(
	sources []SampledSource,
	mainID string,
	options MatchOptions,
	alignments map[string]ConstantAlignment,
	previous []MatchedRow,
) (MatchResult, error) {
	if len(options.MainMoments) != 0 || options.DarkCount != 0 || options.MotionCount != 0 {
		return MatchResult{}, errors.New("aligned matching requires automatic scenes without quotas")
	}
	for _, source := range sources {
		alignment, ok := alignments[source.ID]
		if !source.PreferBFrames || len(source.Anchors) != 0 ||
			source.ID != mainID &&
				(!ok || !matchingUnit(alignment.ToleranceSeconds) || alignment.ToleranceSeconds == 0 || math.IsNaN(alignment.OffsetSeconds) || math.IsInf(alignment.OffsetSeconds, 0)) {
			return MatchResult{}, errors.New("aligned matching requires checked offsets and default picture preferences")
		}
	}
	for _, row := range previous {
		if !row.Complete || len(row.Frames) != len(sources) {
			return MatchResult{}, errors.New("prior aligned rows must be complete and use the same columns")
		}
		for i, frame := range row.Frames {
			if frame.SourceID != sources[i].ID {
				return MatchResult{}, errors.New("prior aligned rows must retain column order")
			}
		}
	}
	return match(sources, mainID, options, alignments, previous)
}

func matchAlignedFrame(main matchingFrame, column matchingColumn) FrameMatch {
	predicted := main.frame.TimestampSeconds + column.alignment.OffsetSeconds
	best := 0.0
	for _, frame := range column.frames {
		if math.Abs(frame.frame.TimestampSeconds-predicted) <= .25 {
			best = max(best, visualScore(main, frame))
		}
	}
	for _, kind := range preferredPictureTypes(main.frame.FrameType) {
		chosen, distance := -1, math.Inf(1)
		for i, frame := range column.frames {
			delta := math.Abs(frame.frame.TimestampSeconds - predicted)
			if frame.frame.FrameType == kind && delta <= .25 && delta < distance && visualScore(main, frame) >= max(.92, best-.03) {
				chosen, distance = i, delta
			}
		}
		if chosen >= 0 {
			return matchedSample(column.source, column.frames[chosen].frame, "aligned", visualScore(main, column.frames[chosen]))
		}
	}
	return FrameMatch{
		SourceID: column.source.ID,
		Reason:   "no_compatible_picture_type",
		Method:   "aligned",
	}
}
