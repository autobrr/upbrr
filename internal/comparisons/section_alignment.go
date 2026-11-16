// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
)

// MatchSceneSection uses only the main input's scene measurements. Other inputs
// supply native pictures, clocks and picture types; their visual correlation is
// correspondence evidence and never a motion/dark/flat ranking score.
//
// A section can contain several useful native moments. Try those already
// decoded moments before seeking another section, retaining the same native
// proof floors. Prefer matching picture types within ten scene-score points;
// otherwise use the best permitted B/P combination. Explicit type choices stay
// authoritative. No offset prediction alone authorizes a picture.
func MatchSceneSection(
	ctx context.Context,
	sources []SampledSource,
	mainID string,
	anchor FrameSample,
	category int,
	scales map[string]PlaybackScale,
	previous []MatchedRow,
) (MatchResult, error) {
	main := slices.IndexFunc(sources, func(s SampledSource) bool { return s.ID == mainID })
	if main < 0 {
		return MatchResult{}, nil
	}
	start, end := SceneSectionBounds(sources[main].Frames, anchor.FrameID)
	section := sources[main].Frames[start:end]
	onlyI := make([]bool, len(sources))
	for i, source := range sources {
		onlyI[i] = len(source.Frames) > 0 && !slices.ContainsFunc(source.Frames, func(frame FrameSample) bool { return frame.FrameType != "I" })
	}
	permitted := func(frame FrameSample) bool {
		if sources[main].PreferBFrames {
			return frame.FrameType == "B" || frame.FrameType == "P" || frame.FrameType == "I" && onlyI[main]
		}
		return allowedPicture(sources[main], frame.FrameType)
	}
	qualified := category >= 0 && slices.ContainsFunc(section, func(frame FrameSample) bool {
		return permitted(frame) && SceneQualified(frame.Evidence, category)
	})
	var candidates []FrameSample
	for _, frame := range section {
		if !permitted(frame) {
			continue
		}
		if qualified && !SceneQualified(frame.Evidence, category) {
			continue
		}
		candidates = append(candidates, frame)
	}
	if len(candidates) == 0 {
		return MatchResult{}, nil
	}
	slices.SortStableFunc(candidates, func(a, b FrameSample) int {
		if category >= 0 {
			if sceneLess(a, b, category) {
				return -1
			}
			if sceneLess(b, a, category) {
				return 1
			}
		}
		if math.Abs(a.TimestampSeconds-anchor.TimestampSeconds) < math.Abs(b.TimestampSeconds-anchor.TimestampSeconds) {
			return -1
		}
		if math.Abs(a.TimestampSeconds-anchor.TimestampSeconds) > math.Abs(b.TimestampSeconds-anchor.TimestampSeconds) {
			return 1
		}
		return cmp.Compare(a.FrameID, b.FrameID)
	})
	proofs := make(map[string][]ConstantAlignment, len(sources)-1)
	for i, source := range sources {
		if i == main {
			continue
		}
		var err error
		proofs[source.ID], err = sectionProofs(ctx, section, source.Frames, scales[source.ID], anchor.TimestampSeconds, candidates)
		if err != nil {
			return MatchResult{}, err
		}
		if len(proofs[source.ID]) == 0 {
			return MatchResult{}, nil
		}
	}
	var preferred, fallback *MatchedRow
	bestScore := math.Inf(-1)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return MatchResult{}, fmt.Errorf("match native section: %w", err)
		}
		local := make(map[string]ConstantAlignment, len(proofs))
		for id, alternatives := range proofs {
			if alignment, ok := momentProof(alternatives, candidate.FrameID); ok {
				local[id] = alignment
			}
		}
		if len(local) != len(sources)-1 {
			continue
		}
		matched, err := AlignedMatch(sources, mainID, MatchOptions{Count: 1, MainFrameID: candidate.FrameID}, local, previous)
		if err != nil {
			return MatchResult{}, err
		}
		if len(matched.Rows) != 1 || !matched.Rows[0].Complete {
			continue
		}
		row := matched.Rows[0]
		agreed, permitsFallback := true, true
		for i, frame := range row.Frames {
			agreed = agreed && frame.FrameType != "" && frame.FrameType == candidate.FrameType
			if frame.FrameType == "I" && sources[i].PreferBFrames && !onlyI[i] {
				agreed = false
			}
			if frame.FrameType != "B" && frame.FrameType != "P" {
				// Explicit choices can permit I alongside other picture types.
				permitsFallback = permitsFallback && frame.FrameType == "I" && !sources[i].PreferBFrames && allowedPicture(sources[i], frame.FrameType)
			}
		}
		if (agreed || permitsFallback) && math.IsInf(bestScore, -1) {
			bestScore = SceneScore(row, category)
		}
		if agreed && SceneScore(row, category) >= bestScore-.1 && (preferred == nil || PreferSceneRow(row, *preferred, category)) {
			preferred = &row
		}
		if permitsFallback && fallback == nil {
			fallback = &row
		}
		if preferred != nil && fallback != nil && picturePreferenceRank(*preferred) == 0 {
			break
		}
	}
	chosen := fallback
	if preferred != nil && SceneScore(*preferred, category) >= bestScore-.1 && (fallback == nil || PreferSceneRow(*preferred, *fallback, category)) {
		chosen = preferred
	}
	if chosen == nil {
		return MatchResult{}, nil
	}
	result := MatchResult{Rows: []MatchedRow{*chosen}}
	if SceneQualified(chosen.Evidence, 0) {
		result.AchievedMotion = 1
	}
	if SceneQualified(chosen.Evidence, 1) {
		result.AchievedDark = 1
	}
	if SceneQualified(chosen.Evidence, 2) {
		result.AchievedFlat = 1
	}
	if chosen == fallback && preferred != fallback {
		result.Warnings = []string{"Some rows use mixed permitted picture types because comparable matching-type pictures were unavailable."}
	}
	return result, nil
}

// SceneSectionBounds retains the observed, eligible native run containing the
// seed. A transition, missing interval or authored occurrence ends that scene.
func SceneSectionBounds(frames []FrameSample, anchor string) (int, int) {
	index := slices.IndexFunc(frames, func(f FrameSample) bool { return f.FrameID == anchor })
	if index < 0 || frames[index].Evidence == nil || !frames[index].Evidence.Eligible {
		return 0, 0
	}
	continuous := func(a, b FrameSample) bool {
		return a.Evidence != nil && b.Evidence != nil && a.Evidence.Eligible && b.Evidence.Eligible && a.Occurrence == b.Occurrence &&
			b.TimestampSeconds > a.TimestampSeconds && b.TimestampSeconds-a.TimestampSeconds <= max(.15, a.DisplayEndSeconds-a.TimestampSeconds+a.TimebaseTickSeconds)+1e-9
	}
	start, end := index, index+1
	for start > 0 && continuous(frames[start-1], frames[start]) {
		start--
	}
	for end < len(frames) && continuous(frames[end-1], frames[end]) {
		end++
	}
	return start, end
}

// MatchingPictureTypes reports agreement across the complete native row.
func MatchingPictureTypes(row MatchedRow) bool {
	return row.Complete && len(row.Frames) > 0 && row.Frames[0].FrameType != "" &&
		!slices.ContainsFunc(row.Frames, func(frame FrameMatch) bool { return frame.FrameType != row.Frames[0].FrameType })
}

// SceneScore is main-input quality, independent of native correspondence scores.
func SceneScore(row MatchedRow, category int) float64 {
	if row.Evidence == nil || category < 0 {
		return 0
	}
	return sceneCategory(row.Evidence, category).Score
}

// PreferSceneRow trades at most ten main-scene score points for type agreement.
// Both rows must already have passed the unchanged native-picture checks.
func PreferSceneRow(candidate, current MatchedRow, category int) bool {
	a, b := SceneScore(candidate, category), SceneScore(current, category)
	if MatchingPictureTypes(candidate) != MatchingPictureTypes(current) {
		if MatchingPictureTypes(candidate) {
			return a >= b-.1
		}
		return a > b+.1
	}
	if MatchingPictureTypes(candidate) && picturePreferenceRank(candidate) != picturePreferenceRank(current) {
		if picturePreferenceRank(candidate) < picturePreferenceRank(current) {
			return a >= b-.1
		}
		return a > b+.1
	}
	return a > b
}

func picturePreferenceRank(row MatchedRow) int {
	rank := 0
	for _, frame := range row.Frames {
		if frame.TypeFallback {
			if frame.FrameType == "I" {
				rank += 2
			} else {
				rank++
			}
		}
	}
	return rank
}

func sectionProofs(
	ctx context.Context,
	reference, target []FrameSample,
	scale PlaybackScale,
	center float64,
	candidates []FrameSample,
) ([]ConstantAlignment, error) {
	if len(reference) < 4 || len(target) < 4 {
		return nil, nil
	}
	type span struct{ start, end float64 }
	windows := []span{{reference[0].TimestampSeconds, reference[len(reference)-1].TimestampSeconds + 1e-9}}
	for _, duration := range []float64{2, 1.25} {
		for _, shift := range []float64{0, -.5, .5, -1, 1} {
			windows = append(windows, span{center + shift - duration/2, center + shift + duration/2})
		}
		for point := reference[0].TimestampSeconds + duration/2; point < reference[len(reference)-1].TimestampSeconds; point++ {
			windows = append(windows, span{point - duration/2, point + duration/2})
		}
	}
	var result []ConstantAlignment
	for _, window := range windows {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("verify native section: %w", err)
		}
		var frames []FrameSample
		for _, frame := range reference {
			if frame.TimestampSeconds >= window.start && frame.TimestampSeconds <= window.end {
				frames = append(frames, frame)
			}
		}
		if len(frames) < 4 {
			continue
		}
		alignment, reason := DiscoverPlaybackAlignment(frames, target, scale)
		if reason != "" {
			continue
		}
		if alignment.IntervalProof || VerifyAlignedMomentContext(frames, target, frames[0].FrameID, alignment) {
			alignment.ReferenceFrameIDs = make([]string, len(frames))
			for i := range frames {
				alignment.ReferenceFrameIDs[i] = frames[i].FrameID
			}
		}
		result = append(result, alignment)
		if !slices.ContainsFunc(candidates, func(frame FrameSample) bool {
			_, verified := momentProof(result, frame.FrameID)
			return !verified
		}) {
			break
		}
	}
	return result, nil
}

func momentProof(alternatives []ConstantAlignment, id string) (ConstantAlignment, bool) {
	var result ConstantAlignment
	found := false
	for _, alignment := range alternatives {
		if !slices.Contains(alignment.ReferenceFrameIDs, id) {
			continue
		}
		if !found {
			result, found = alignment, true
			continue
		}
		if result.Scale() != alignment.Scale() || result.IntervalProof != alignment.IntervalProof {
			return ConstantAlignment{}, false
		}
		if result.IntervalProof {
			lower := max(result.OffsetSeconds-result.ToleranceSeconds, alignment.OffsetSeconds-alignment.ToleranceSeconds)
			upper := min(result.OffsetSeconds+result.ToleranceSeconds, alignment.OffsetSeconds+alignment.ToleranceSeconds)
			if lower > upper {
				return ConstantAlignment{}, false
			}
			result.OffsetSeconds, result.ToleranceSeconds = (lower+upper)/2, max(1e-9, (upper-lower)/2)
		} else if !result.Consistent(alignment) {
			return ConstantAlignment{}, false
		}
	}
	return result, found
}
