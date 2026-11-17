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
	Points                           []AlignmentPoint
	ReferenceFrameIDs                []string
	ScaleNumerator, ScaleDenominator int64
	IntervalProof                    bool
	OffsetSeconds                    float64
	ToleranceSeconds                 float64
}

// AlignmentPoint retains an independently observed native transition's residual
// limit for validation against the fitted offset, including the held-out region.
type AlignmentPoint struct{ OffsetSeconds, ToleranceSeconds float64 }

func (a ConstantAlignment) validatesOffset(offset float64) bool {
	if math.Abs(offset-a.OffsetSeconds) > a.ToleranceSeconds {
		return false
	}
	for _, point := range a.Points {
		if math.Abs(offset-point.OffsetSeconds) > point.ToleranceSeconds {
			return false
		}
	}
	return true
}

// Consistent reports agreement within the finer observed subframe tolerance.
func (a ConstantAlignment) Consistent(b ConstantAlignment) bool {
	return a.Scale() == b.Scale() && a.IntervalProof == b.IntervalProof &&
		math.Abs(a.OffsetSeconds-b.OffsetSeconds) <= min(a.ToleranceSeconds, b.ToleranceSeconds)
}

// Scale returns the exact selected rational's search-boundary representation.
// Zero numerator/denominator decodes retained scale-one callers compatibly.
func (a ConstantAlignment) Scale() float64 {
	if a.ScaleNumerator == 0 && a.ScaleDenominator == 0 {
		return 1
	}
	if a.ScaleNumerator <= 0 || a.ScaleDenominator <= 0 {
		return math.NaN()
	}
	return float64(a.ScaleNumerator) / float64(a.ScaleDenominator)
}

// Predict maps title-relative presentation time, never decoder frame ordinals.
func (a ConstantAlignment) Predict(seconds float64) float64 {
	return a.Scale()*seconds + a.OffsetSeconds
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

// AlignedMatch consumes checked offsets and an optional exact main identity.
// Scene discovery and quotas stay with the main selector. Exact manual anchors
// authorize only their observed moment; explicit picture preferences are retained.
// Target reuse is still rejected; static scenes cannot drift for a higher score.
func AlignedMatch(
	sources []SampledSource,
	mainID string,
	options MatchOptions,
	alignments map[string]ConstantAlignment,
	previous []MatchedRow,
) (MatchResult, error) {
	if len(options.MainMoments) != 0 || options.DarkCount != 0 || options.MotionCount != 0 || options.FlatCount != 0 {
		return MatchResult{}, errors.New("aligned matching requires automatic scenes without quotas")
	}
	var selected *FrameSample
	for _, source := range sources {
		if source.ID == mainID && options.MainFrameID != "" {
			for i := range source.Frames {
				if source.Frames[i].FrameID == options.MainFrameID {
					selected = &source.Frames[i]
					break
				}
			}
		}
	}
	for _, source := range sources {
		alignment, ok := alignments[source.ID]
		exactAnchor := selected != nil && slices.ContainsFunc(source.Anchors, func(anchor MatchAnchor) bool {
			return math.Abs(anchor.MainSeconds-selected.TimestampSeconds) < 1e-6
		})
		if len(source.Anchors) != 0 && options.MainFrameID == "" ||
			source.ID != mainID && !exactAnchor &&
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
	predicted := column.alignment.Predict(main.frame.TimestampSeconds)
	within := func(frame FrameSample) bool {
		if !column.alignment.IntervalProof {
			return math.Abs(frame.TimestampSeconds-predicted) <= column.alignment.ToleranceSeconds
		}
		if main.frame.DisplayEndSeconds <= main.frame.TimestampSeconds || frame.DisplayEndSeconds <= frame.TimestampSeconds {
			return false
		}
		return frame.TimestampSeconds <= column.alignment.Predict(main.frame.DisplayEndSeconds)+frame.TimebaseTickSeconds &&
			frame.DisplayEndSeconds >= predicted-frame.TimebaseTickSeconds
	}
	best := 0.0
	for _, frame := range column.frames {
		if within(frame.frame) {
			best = max(best, visualScore(main, frame))
		}
	}
	types := preferredPictureTypes(main.frame.FrameType)
	if column.source.IntraOnly && !slices.Contains(types, "I") {
		types = append(types, "I")
	}
	if !column.source.PreferBFrames {
		types = column.source.AllowedTypes
		if len(types) == 0 {
			types = []string{"B", "P", "I", ""}
		}
	}
	for _, kind := range types {
		chosen, distance := -1, math.Inf(1)
		for i, frame := range column.frames {
			delta := math.Abs(frame.frame.TimestampSeconds - predicted)
			if frame.frame.FrameType == kind && within(frame.frame) && delta < distance && visualScore(main, frame) >= max(.92, best-.03) {
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

// VerifyAlignedMomentContext verifies an interior native picture against the
// complete local sequence. The local temporal search must independently resolve
// the same offset without a repeated alternative; every native pair then has to
// agree, with consecutive identities, cadence and occurrence continuity. This
// admits quiet interiors without extrapolating a point proof outside its window.
func VerifyAlignedMomentContext(reference, target []FrameSample, frameID string, alignment ConstantAlignment) bool {
	index := slices.IndexFunc(reference, func(f FrameSample) bool { return f.FrameID == frameID })
	if index < 0 || alignment.IntervalProof || !matchingUnit(alignment.ToleranceSeconds) || alignment.ToleranceSeconds == 0 ||
		math.IsNaN(alignment.Predict(0)) ||
		math.IsInf(alignment.Predict(0), 0) ||
		math.IsNaN(alignment.Scale()) {
		return false
	}
	transformed := slices.Clone(reference)
	for i := range transformed {
		transformed[i].TimestampSeconds *= alignment.Scale()
	}
	checked, reason := DiscoverAlignment(transformed, target)
	if reason != "" || !alignment.validatesOffset(checked.OffsetSeconds) {
		return false
	}
	columns, _, err := prepareMatching([]SampledSource{{ID: "main", Frames: reference}, {ID: "target", Frames: target}}, "main", MatchOptions{Count: 1})
	if err != nil {
		return false
	}
	previous := -1
	occurrence := ""
	for i := range reference {
		frame := reference[i]
		if !frame.NativeSequence || frame.FrameID == "" || frame.Occurrence != reference[index].Occurrence {
			return false
		}
		predicted := alignment.Predict(frame.TimestampSeconds)
		chosen := -1
		for j, f := range target {
			if math.Abs(f.TimestampSeconds-predicted) <= alignment.ToleranceSeconds {
				if chosen >= 0 {
					return false
				}
				chosen = j
			}
		}
		if chosen < 0 || previous >= 0 && chosen != previous+1 || !target[chosen].NativeSequence || target[chosen].FrameID == "" {
			return false
		}
		if previous < 0 {
			occurrence = target[chosen].Occurrence
		} else if target[chosen].Occurrence != occurrence {
			return false
		}
		if visualScore(columns[0].frames[i], columns[1].frames[chosen]) < .92 {
			return false
		}
		previous = chosen
	}
	return true
}
