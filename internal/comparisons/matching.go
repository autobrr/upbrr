// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// FrameSample describes one actually decoded frame. Luma grids must share their
// dimensions and aspect treatment across sources. Brightness and Motion are
// normalized thumbnail measurements in [0,1], not physical luminance claims.
type FrameSample struct {
	TimestampSeconds float64
	FrameType        string
	Luma             []uint8
	Brightness       float64
	Motion           float64
}

// MatchAnchor records explicitly inspected correspondence. Only a selected main
// moment exactly at an anchor bypasses automatic visual matching.
type MatchAnchor struct {
	MainSeconds   float64
	SourceSeconds float64
}

// SampledSource carries ordered presentation-time samples for one column.
// Empty AllowedTypes accepts any picture type; an empty FrameType stays unknown.
type SampledSource struct {
	ID           string
	Frames       []FrameSample
	AllowedTypes []string
	Anchors      []MatchAnchor
}

// MatchOptions selects automatic scenes or explicit main-source moments.
// MainMoments overrides Count. Fractions request scene coverage in [0,1].
type MatchOptions struct {
	Count          int
	MainMoments    []float64
	DarkFraction   float64
	MotionFraction float64
}

// FrameMatch retains the actual proposed frame, including unresolved proposals
// useful for review. Confidence is a visual correlation score, not a calibrated
// probability; manual correspondence has no inferred visual confidence.
type FrameMatch struct {
	SourceID         string
	TimestampSeconds float64
	FrameType        string
	Method           string
	Confidence       float64
	Resolved         bool
	Reason           string
	TypeFallback     bool
	RequestedTypes   []string
}

// MatchedRow preserves source order for one common visual moment.
type MatchedRow struct {
	MainSeconds float64
	Frames      []FrameMatch
	Complete    bool
	Scene       string
}

// MatchResult reports established rows separately from unmet scene preferences.
// Every row still requires the workflow's explicit image review before upload.
type MatchResult struct {
	Rows            []MatchedRow
	RequestedDark   int
	RequestedMotion int
	AchievedDark    int
	AchievedMotion  int
	Warnings        []string
}

// Match proposes correspondence without mutating samples or performing IO.
// Every automatic moment is searched independently across the sampled timelines;
// anchors never authorize extrapolation through an unverified edit. Automatic
// selection tries at most 64 extra scenes before returning unresolved proposals.
// Invalid source IDs, samples, moments or preferences return an error.
func Match(sources []SampledSource, mainID string, options MatchOptions) (MatchResult, error) {
	columns, main, err := prepareMatching(sources, mainID, options)
	if err != nil {
		return MatchResult{}, err
	}
	count := options.Count
	if len(options.MainMoments) > 0 {
		count = len(options.MainMoments)
	}
	result := MatchResult{
		RequestedDark:   int(math.Ceil(float64(count) * options.DarkFraction)),
		RequestedMotion: int(math.Ceil(float64(count) * options.MotionFraction)),
	}
	appendRow := func(row MatchedRow, frame FrameSample) {
		result.Rows = append(result.Rows, row)
		if row.Complete {
			if frame.Brightness <= .25 {
				result.AchievedDark++
			}
			if frame.Motion >= .15 {
				result.AchievedMotion++
			}
		}
	}
	used := map[int]bool{}
	if len(options.MainMoments) > 0 {
		for _, moment := range options.MainMoments {
			index := nearestSample(columns[main].frames, moment)
			if used[index] {
				return MatchResult{}, errors.New("comparison moments resolve to the same sampled frame")
			}
			used[index] = true
			appendRow(matchRow(columns, main, index, result.Rows), columns[main].frames[index].frame)
		}
	} else {
		var unresolved []MatchedRow
		for range min(count, len(columns[main].frames)) + 64 {
			index := chooseScene(columns[main], used, result, count)
			if index < 0 || len(result.Rows) == count {
				break
			}
			used[index] = true
			row := matchRow(columns, main, index, result.Rows)
			if row.Complete {
				appendRow(row, columns[main].frames[index].frame)
			} else {
				unresolved = append(unresolved, row)
			}
		}
		for _, row := range unresolved {
			if len(result.Rows) == count {
				break
			}
			result.Rows = append(result.Rows, row)
		}
		slices.SortFunc(result.Rows, func(a, b MatchedRow) int { return cmp.Compare(a.MainSeconds, b.MainSeconds) })
	}
	if len(result.Rows) < count {
		result.Warnings = append(result.Warnings, "Not enough useful sampled scenes were available for the requested comparison count.")
	}
	if slices.ContainsFunc(result.Rows, func(row MatchedRow) bool { return !row.Complete }) {
		result.Warnings = append(result.Warnings, "Some comparison moments need manual correspondence or replacement.")
	}
	if result.AchievedDark < result.RequestedDark || result.AchievedMotion < result.RequestedMotion {
		result.Warnings = append(result.Warnings, "The requested dark or motion scene mix was not fully established in complete comparison rows.")
	}
	return result, nil
}

type matchingFrame struct {
	frame     FrameSample
	signature []float64
}

type matchingColumn struct {
	source SampledSource
	frames []matchingFrame
}

func prepareMatching(sources []SampledSource, mainID string, options MatchOptions) ([]matchingColumn, int, error) {
	if len(sources) < 2 || (options.Count < 1 && len(options.MainMoments) == 0) ||
		!matchingUnit(options.DarkFraction) || !matchingUnit(options.MotionFraction) {
		return nil, 0, errors.New("comparison matching requires multiple sources, a positive count and valid scene fractions")
	}
	columns := make([]matchingColumn, len(sources))
	main, pixels := -1, 0
	seen := map[string]bool{}
	for i, source := range sources {
		if source.ID == "" || seen[source.ID] {
			return nil, 0, errors.New("comparison matching requires distinct source IDs")
		}
		seen[source.ID] = true
		if source.ID == mainID {
			main = i
		}
		for _, kind := range source.AllowedTypes {
			if kind != "I" && kind != "P" && kind != "B" {
				return nil, 0, errors.New("comparison allowed picture types must be I, P or B")
			}
		}
		column := matchingColumn{source: source, frames: make([]matchingFrame, len(source.Frames))}
		for j, frame := range source.Frames {
			if !matchingTime(frame.TimestampSeconds) || !matchingUnit(frame.Brightness) || !matchingUnit(frame.Motion) ||
				(j > 0 && frame.TimestampSeconds <= source.Frames[j-1].TimestampSeconds) || len(frame.Luma) < 16 {
				return nil, 0, fmt.Errorf("comparison source %q has invalid or unordered decoded samples", source.ID)
			}
			if pixels == 0 {
				pixels = len(frame.Luma)
			}
			if len(frame.Luma) != pixels {
				return nil, 0, errors.New("comparison luminance samples must have the same grid dimensions")
			}
			column.frames[j] = matchingFrame{frame: frame, signature: matchingSignature(frame.Luma)}
		}
		anchorTimes := map[float64]bool{}
		for _, anchor := range source.Anchors {
			if !matchingTime(anchor.MainSeconds) || !matchingTime(anchor.SourceSeconds) || anchorTimes[anchor.MainSeconds] {
				return nil, 0, errors.New("comparison anchors require distinct main moments and valid source times")
			}
			anchorTimes[anchor.MainSeconds] = true
		}
		columns[i] = column
	}
	if main < 0 || len(columns[main].frames) == 0 {
		return nil, 0, errors.New("comparison main source has no decoded samples")
	}
	for _, moment := range options.MainMoments {
		if !matchingTime(moment) || moment < columns[main].frames[0].frame.TimestampSeconds ||
			moment > columns[main].frames[len(columns[main].frames)-1].frame.TimestampSeconds {
			return nil, 0, errors.New("comparison main moment is outside the sampled timeline")
		}
	}
	return columns, main, nil
}

func matchingTime(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func matchingUnit(value float64) bool { return matchingTime(value) && value <= 1 }

// Zero-mean unit vectors compare spatial structure despite brightness/contrast
// changes. Flat thumbnails cannot establish correspondence, even when dark.
func matchingSignature(luma []byte) []float64 {
	mean := 0.0
	for _, value := range luma {
		mean += float64(value)
	}
	mean /= float64(len(luma))
	values := make([]float64, len(luma))
	squared := 0.0
	for i, value := range luma {
		values[i] = float64(value) - mean
		squared += values[i] * values[i]
	}
	if squared/float64(len(luma)) < 4 {
		return nil
	}
	norm := math.Sqrt(squared)
	for i := range values {
		values[i] /= norm
	}
	return values
}

func visualScore(a, b matchingFrame) float64 {
	if len(a.signature) == 0 || len(b.signature) == 0 {
		return 0
	}
	score := 0.0
	for i, value := range a.signature {
		score += value * b.signature[i]
	}
	return max(0, min(1, score))
}

func matchRow(columns []matchingColumn, main, index int, selected []MatchedRow) MatchedRow {
	frame := columns[main].frames[index]
	row := MatchedRow{
		MainSeconds: frame.frame.TimestampSeconds,
		Complete:    true,
		Frames:      make([]FrameMatch, len(columns)),
	}
	if frame.frame.Brightness <= .25 {
		row.Scene = "dark"
	}
	if frame.frame.Motion >= .15 {
		if row.Scene != "" {
			row.Scene += ","
		}
		row.Scene += "motion"
	}
	for i, column := range columns {
		if i == main {
			row.Frames[i] = matchedSample(column.source, frame.frame, "main", 1)
		} else {
			matched := matchFrame(frame, column)
			if matched.Resolved && matched.Method == "visual" {
				target := column.frames[nearestSample(column.frames, matched.TimestampSeconds)]
				reused := slices.ContainsFunc(selected, func(row MatchedRow) bool {
					return row.Complete && math.Abs(row.Frames[i].TimestampSeconds-matched.TimestampSeconds) < 1e-6
				})
				if reused || ambiguousVisualMatch(target, columns[main].frames, frame.frame.TimestampSeconds, matched.Confidence) {
					matched.Resolved, matched.Confidence, matched.Reason = false, 0, "ambiguous_visual_match"
				}
			}
			row.Frames[i] = matched
		}
		row.Complete = row.Complete && row.Frames[i].Resolved
	}
	return row
}

func matchFrame(main matchingFrame, column matchingColumn) FrameMatch {
	expected := main.frame.TimestampSeconds
	distance := math.Inf(1)
	for _, anchor := range column.source.Anchors {
		if math.Abs(anchor.MainSeconds-main.frame.TimestampSeconds) < 1e-6 {
			index := nearestSample(column.frames, anchor.SourceSeconds)
			if index >= 0 && math.Abs(column.frames[index].frame.TimestampSeconds-anchor.SourceSeconds) < 1e-6 {
				return matchedSample(column.source, column.frames[index].frame, "manual", 0)
			}
			return FrameMatch{
				SourceID: column.source.ID,
				Method:   "manual",
				Reason:   "manual_frame_unavailable",
			}
		}
		if delta := math.Abs(anchor.MainSeconds - main.frame.TimestampSeconds); delta < distance {
			distance = delta
			expected = main.frame.TimestampSeconds + anchor.SourceSeconds - anchor.MainSeconds
		}
	}
	best, bestScore := -1, 0.0
	for i, candidate := range column.frames {
		score := visualScore(main, candidate)
		if score > bestScore || best >= 0 && math.Abs(score-bestScore) < 1e-9 &&
			math.Abs(candidate.frame.TimestampSeconds-expected) < math.Abs(column.frames[best].frame.TimestampSeconds-expected) {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return FrameMatch{
			SourceID: column.source.ID,
			Method:   "visual",
			Reason:   "no_visual_match",
		}
	}
	matched := matchedSample(column.source, column.frames[best].frame, "visual", bestScore)
	if bestScore < .92 {
		matched.Resolved, matched.TypeFallback, matched.Reason = false, false, "no_visual_match"
		return matched
	}
	if ambiguousVisualMatch(main, column.frames, matched.TimestampSeconds, bestScore) {
		matched.Resolved, matched.TypeFallback, matched.Reason = false, false, "ambiguous_visual_match"
		return matched
	}
	if matched.TypeFallback {
		allowedScore := 0.0
		for _, candidate := range column.frames {
			score := visualScore(main, candidate)
			if math.Abs(candidate.frame.TimestampSeconds-column.frames[best].frame.TimestampSeconds) <= .25 &&
				score >= max(.92, bestScore-.03) && score > allowedScore && allowedPicture(column.source, candidate.frame.FrameType) {
				matched, allowedScore = matchedSample(column.source, candidate.frame, "visual", score), score
			}
		}
		if ambiguousVisualMatch(main, column.frames, matched.TimestampSeconds, matched.Confidence) {
			matched.Resolved, matched.TypeFallback, matched.Reason = false, false, "ambiguous_visual_match"
		}
	}
	return matched
}

// Both directions must distinguish distant repeated scenes before a row is
// counted, allowing the existing selector to try another common moment.
func ambiguousVisualMatch(reference matchingFrame, frames []matchingFrame, seconds, score float64) bool {
	return slices.ContainsFunc(frames, func(candidate matchingFrame) bool {
		return math.Abs(candidate.frame.TimestampSeconds-seconds) > .5 && visualScore(reference, candidate) >= score-.03
	})
}

func matchedSample(source SampledSource, frame FrameSample, method string, confidence float64) FrameMatch {
	match := FrameMatch{
		SourceID:         source.ID,
		TimestampSeconds: frame.TimestampSeconds,
		FrameType:        strings.ToUpper(strings.TrimSpace(frame.FrameType)),
		Method:           method,
		Confidence:       confidence,
		Resolved:         true,
		RequestedTypes:   slices.Clone(source.AllowedTypes),
		TypeFallback:     !allowedPicture(source, frame.FrameType),
	}
	if match.TypeFallback {
		match.Reason = "requested_picture_type_unavailable_at_match"
	}
	return match
}

func allowedPicture(source SampledSource, kind string) bool {
	return len(source.AllowedTypes) == 0 || slices.Contains(source.AllowedTypes, strings.ToUpper(strings.TrimSpace(kind)))
}

func nearestSample(frames []matchingFrame, seconds float64) int {
	best := -1
	for i, candidate := range frames {
		if best < 0 || math.Abs(candidate.frame.TimestampSeconds-seconds) < math.Abs(frames[best].frame.TimestampSeconds-seconds) {
			best = i
		}
	}
	return best
}

func chooseScene(main matchingColumn, used map[int]bool, result MatchResult, count int) int {
	best, bestScore := -1, math.Inf(-1)
	duration := main.frames[len(main.frames)-1].frame.TimestampSeconds - main.frames[0].frame.TimestampSeconds
	for i, candidate := range main.frames {
		if used[i] || len(candidate.signature) == 0 {
			continue
		}
		score := 0.0
		if allowedPicture(main.source, candidate.frame.FrameType) {
			score += 2
		}
		if result.AchievedDark < result.RequestedDark && candidate.frame.Brightness <= .25 {
			score += 4 + 1 - candidate.frame.Brightness
		}
		if result.AchievedMotion < result.RequestedMotion && candidate.frame.Motion >= .15 {
			score += 4 + candidate.frame.Motion
		}
		distance := duration
		for _, row := range result.Rows {
			distance = min(distance, math.Abs(candidate.frame.TimestampSeconds-row.MainSeconds))
		}
		if len(result.Rows) > 0 && distance < duration/float64(max(1, count*3)) {
			continue
		}
		if duration > 0 {
			score += distance / duration
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}
