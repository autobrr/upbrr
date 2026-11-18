// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
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
	matcher := NewSceneSectionMatcher(sources[main], anchor, category)
	order := make([]string, len(sources))
	for i, source := range sources {
		order[i] = source.ID
		if i == main {
			continue
		}
		viable, err := matcher.AddSource(ctx, source, scales[source.ID])
		if err != nil || !viable {
			return MatchResult{}, err
		}
	}
	return matcher.Match(ctx, order, previous)
}

// SceneSectionMatcher owns attempt-local native proofs. Adding each decoded
// source rejects an impossible row before the caller opens later inputs. The
// same proofs authorize the final intersection; none survive this native window.
type SceneSectionMatcher struct {
	mainID              string
	anchor              FrameSample
	category            int
	section, candidates []FrameSample
	sources             map[string]SampledSource
	proofs              map[string]map[string]sceneSectionProof
	surviving           []FrameSample
	reason              string
}

// NewSceneSectionMatcher binds immutable decoded main pictures and scene intent.
func NewSceneSectionMatcher(reference SampledSource, anchor FrameSample, category int) *SceneSectionMatcher {
	matcher := &SceneSectionMatcher{
		mainID:   reference.ID,
		anchor:   anchor,
		category: category,
		sources:  map[string]SampledSource{reference.ID: reference},
		proofs:   map[string]map[string]sceneSectionProof{},
	}
	start, end := 0, len(reference.Frames)
	if category >= 0 {
		start, end = SceneSectionBounds(reference.Frames, anchor.FrameID)
	}
	section := reference.Frames[start:end]
	var candidates []FrameSample
	for _, frame := range section {
		if frame.Unselectable || !slices.Contains(permittedPictureTypes(reference), frame.FrameType) || frame.Evidence == nil || !frame.Evidence.Eligible {
			continue
		}
		candidates = append(candidates, frame)
	}
	if len(candidates) == 0 {
		matcher.reason = "main_native_candidates_unavailable"
		if slices.ContainsFunc(section, func(frame FrameSample) bool { return frame.Evidence != nil && frame.Evidence.Eligible }) {
			matcher.reason = "permitted_picture_type_unavailable"
		}
		return matcher
	}
	// The connected score band around the main seed is the first source of
	// truth. Other eligible moments in this same observed scene remain best
	// effort alternatives when the band cannot supply a complete native row.
	bandStart, bandEnd := sceneScoreBand(section, anchor, category)
	inBand := func(frame FrameSample) bool {
		return bandStart <= frame.TimestampSeconds && frame.TimestampSeconds <= bandEnd
	}
	slices.SortStableFunc(candidates, func(a, b FrameSample) int {
		if category >= 0 {
			if SceneQualified(a.Evidence, category) != SceneQualified(b.Evidence, category) {
				if SceneQualified(a.Evidence, category) {
					return -1
				}
				return 1
			}
			if inBand(a) != inBand(b) {
				if inBand(a) {
					return -1
				}
				return 1
			}
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
	matcher.section, matcher.candidates = section, candidates
	matcher.surviving = slices.Clone(candidates)
	return matcher
}

// AddSource checks fresh native correspondence before any later input is decoded.
func (m *SceneSectionMatcher) AddSource(ctx context.Context, source SampledSource, scale PlaybackScale) (bool, error) {
	if _, exists := m.sources[source.ID]; exists {
		return false, fmt.Errorf("native comparison section repeats source %q", source.ID)
	}
	if len(m.candidates) == 0 {
		return false, nil
	}
	proofs, err := sectionProofs(ctx, m.section, source.Frames, scale, m.anchor.TimestampSeconds, m.candidates)
	if err != nil || len(proofs) == 0 {
		m.reason = "native_correspondence_unavailable"
		return false, err
	}
	column := make(map[string]sceneSectionProof)
	for _, candidate := range m.candidates {
		if proof, ok := momentProof(proofs, candidate.FrameID); ok {
			column[candidate.FrameID] = proof
		}
	}
	if len(column) == 0 {
		m.reason = "native_correspondence_unavailable"
		return false, nil
	}
	m.sources[source.ID], m.proofs[source.ID] = source, column
	m.surviving = slices.DeleteFunc(m.surviving, func(frame FrameSample) bool {
		_, ok := column[frame.FrameID]
		return !ok
	})
	if len(m.surviving) == 0 {
		m.reason = "native_intersection_unavailable"
		return false, nil
	}
	return true, nil
}

// InconclusiveReason reports already observed proof/type/intersection outcomes;
// diagnostics never initiate another decode or temporal search.
func (m *SceneSectionMatcher) InconclusiveReason() string { return m.reason }

// Match intersects retained proofs in the workflow's original column order,
// preserving previous-row identity reuse checks without another proof search.
func (m *SceneSectionMatcher) Match(ctx context.Context, order []string, previous []MatchedRow) (MatchResult, error) {
	if len(order) != len(m.sources) {
		return MatchResult{}, errors.New("native comparison section source order changed")
	}
	sources := make([]SampledSource, 0, len(order))
	seen := map[string]bool{}
	for _, id := range order {
		source, ok := m.sources[id]
		if !ok || seen[id] {
			return MatchResult{}, errors.New("native comparison section source order changed")
		}
		seen[id] = true
		sources = append(sources, source)
	}
	mainID, category, proofs, candidates := m.mainID, m.category, m.proofs, m.surviving
	var preferred, fallback *MatchedRow
	bestScore := math.Inf(-1)
	qualified := false
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return MatchResult{}, fmt.Errorf("match native section: %w", err)
		}
		local := make(map[string]ConstantAlignment, len(proofs))
		scoped := slices.Clone(sources)
		for i, source := range sources {
			if source.ID == mainID {
				continue
			}
			proof := proofs[source.ID][candidate.FrameID]
			local[source.ID] = proof.ConstantAlignment
			scoped[i].Frames = slices.DeleteFunc(slices.Clone(source.Frames), func(frame FrameSample) bool {
				return frame.Occurrence != proof.targetOccurrence
			})
		}
		matched, err := AlignedMatch(scoped, mainID, MatchOptions{Count: 1, MainFrameID: candidate.FrameID}, local, previous)
		if err != nil {
			return MatchResult{}, err
		}
		if len(matched.Rows) != 1 || !matched.Rows[0].Complete {
			m.reason = "native_correspondence_unavailable"
			for _, row := range matched.Rows {
				if slices.ContainsFunc(row.Frames, func(frame FrameMatch) bool { return frame.Reason == "no_compatible_picture_type" }) {
					m.reason = "permitted_picture_type_unavailable"
				}
			}
			continue
		}
		row := matched.Rows[0]
		agreed, permitsFallback := true, true
		for i, frame := range row.Frames {
			agreed = agreed && frame.FrameType != "" && frame.FrameType == candidate.FrameType
			if frame.FrameType == "I" && sources[i].PreferBFrames && !sources[i].IntraOnly {
				agreed = false
			}
			if frame.FrameType != "B" && frame.FrameType != "P" {
				// Explicit choices can permit I alongside other picture types.
				permitsFallback = permitsFallback && frame.FrameType == "I" &&
					(sources[i].PreferBFrames && sources[i].IntraOnly || !sources[i].PreferBFrames && allowedPicture(sources[i], frame.FrameType))
			}
		}
		if !agreed && !permitsFallback {
			continue
		}
		rowQualified := category >= 0 && SceneQualified(row.Evidence, category)
		if qualified && !rowQualified {
			continue
		}
		if rowQualified && !qualified {
			preferred, fallback, bestScore, qualified = nil, nil, math.Inf(-1), true
		}
		bestScore = max(bestScore, SceneScore(row, category))
		if agreed && SceneScore(row, category) >= bestScore-.1 && (preferred == nil || PreferSceneRow(row, *preferred, category)) {
			preferred = &row
		}
		if permitsFallback && (fallback == nil || SceneScore(row, category) > SceneScore(*fallback, category)) {
			fallback = &row
		}
	}
	chosen := fallback
	if preferred != nil && SceneScore(*preferred, category) >= bestScore-.1 && (fallback == nil || PreferSceneRow(*preferred, *fallback, category)) {
		chosen = preferred
	}
	if chosen == nil {
		if m.reason == "" {
			m.reason = "native_intersection_unavailable"
		}
		return MatchResult{}, nil
	}
	m.reason = ""
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
	if !MatchingPictureTypes(*chosen) {
		result.Warnings = []string{"Some rows use mixed permitted picture types because comparable matching-type pictures were unavailable."}
	}
	return result, nil
}

func sceneScoreBand(section []FrameSample, anchor FrameSample, category int) (float64, float64) {
	index := slices.IndexFunc(section, func(frame FrameSample) bool { return frame.FrameID == anchor.FrameID })
	if category < 0 || index < 0 {
		return math.Inf(-1), math.Inf(1)
	}
	floor := SceneScore(MatchedRow{Evidence: section[index].Evidence}, category) - .1
	eligible := func(frame FrameSample) bool {
		return frame.Evidence != nil && frame.Evidence.Eligible && SceneScore(MatchedRow{Evidence: frame.Evidence}, category) >= floor
	}
	start, end := index, index
	for start > 0 && eligible(section[start-1]) {
		start--
	}
	for end+1 < len(section) && eligible(section[end+1]) {
		end++
	}
	return section[start].TimestampSeconds, section[end].TimestampSeconds
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
	if category >= 0 && SceneQualified(candidate.Evidence, category) != SceneQualified(current.Evidence, category) {
		return SceneQualified(candidate.Evidence, category)
	}
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

// A temporal proof binds a target occurrence as well as its offset. Type
// evidence may be broader; it never widens this correspondence authority.
type sceneSectionProof struct {
	ConstantAlignment
	targetOccurrence string
}

func occurrenceRuns(frames []FrameSample) iter.Seq[[]FrameSample] {
	return func(yield func([]FrameSample) bool) {
		for start := 0; start < len(frames); {
			end := start + 1
			for end < len(frames) && frames[end].Occurrence == frames[start].Occurrence {
				end++
			}
			if !yield(frames[start:end]) {
				return
			}
			start = end
		}
	}
}

func sectionProofs(
	ctx context.Context,
	reference, target []FrameSample,
	scale PlaybackScale,
	center float64,
	candidates []FrameSample,
) ([]sceneSectionProof, error) {
	var result []sceneSectionProof
	for region := range occurrenceRuns(reference) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("verify native occurrences: %w", err)
		}
		if len(region) < 4 {
			continue
		}
		identities := make(map[string]bool, len(region))
		for _, frame := range region {
			identities[frame.FrameID] = true
		}
		var eligible []FrameSample
		for _, frame := range candidates {
			if identities[frame.FrameID] {
				eligible = append(eligible, frame)
			}
		}
		if len(eligible) == 0 {
			continue
		}
		// Every target occurrence is checked. An early success in one cannot
		// hide another authored occurrence supporting the same main picture.
		for observed := range occurrenceRuns(target) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("verify native occurrences: %w", err)
			}
			if len(observed) < 4 {
				continue
			}
			proofs, err := occurrenceSectionProofs(ctx, region, observed, scale, center, eligible)
			if err != nil {
				return nil, err
			}
			result = append(result, proofs...)
		}
	}
	return result, nil
}

func occurrenceSectionProofs(
	ctx context.Context,
	reference, target []FrameSample,
	scale PlaybackScale,
	center float64,
	candidates []FrameSample,
) ([]sceneSectionProof, error) {
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
	var result []sceneSectionProof
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
		result = append(result, sceneSectionProof{ConstantAlignment: alignment, targetOccurrence: target[0].Occurrence})
		if !slices.ContainsFunc(candidates, func(frame FrameSample) bool {
			_, verified := momentProof(result, frame.FrameID)
			return !verified
		}) {
			break
		}
	}
	return result, nil
}

func momentProof(alternatives []sceneSectionProof, id string) (sceneSectionProof, bool) {
	var result sceneSectionProof
	found := false
	for _, alignment := range alternatives {
		if !slices.Contains(alignment.ReferenceFrameIDs, id) {
			continue
		}
		if !found {
			result, found = alignment, true
			continue
		}
		if result.targetOccurrence != alignment.targetOccurrence || result.Scale() != alignment.Scale() || result.IntervalProof != alignment.IntervalProof {
			return sceneSectionProof{}, false
		}
		if result.IntervalProof {
			lower := max(result.OffsetSeconds-result.ToleranceSeconds, alignment.OffsetSeconds-alignment.ToleranceSeconds)
			upper := min(result.OffsetSeconds+result.ToleranceSeconds, alignment.OffsetSeconds+alignment.ToleranceSeconds)
			if lower > upper {
				return sceneSectionProof{}, false
			}
			result.OffsetSeconds, result.ToleranceSeconds = (lower+upper)/2, max(1e-9, (upper-lower)/2)
		} else if !result.Consistent(alignment.ConstantAlignment) {
			return sceneSectionProof{}, false
		}
	}
	return result, found
}
