// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// tryAlignedComparisons owns bounded decoding, not matching policy. Evidence and
// decoded windows live only for this attempt; no filename/duration implies an
// offset. Unsupported or inconclusive evidence returns to the existing scan.
func (b workflowMediaBuilder) tryAlignedComparisons(ctx context.Context, retained workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan, options comparisons.MatchOptions, progress *comparisonProgress,
) (comparisons.MatchResult, map[string][]screenshots.ComparisonSample, bool, error) {
	main := -1
	durations := make([]float64, len(plan.Sources))
	if options.Count < 1 || options.Count > 16 || options.DarkCount != 0 || options.MotionCount != 0 || len(options.MainMoments) != 0 ||
		len(plan.Anchors) != 0 {
		return comparisons.MatchResult{}, nil, false, nil
	}
	for i, source := range plan.Sources {
		private, ok := retained.ComparisonSources[source.ID]
		if !ok || !source.PreferBFrames || len(private.Source.HDRFacts.Formats) != 1 || private.Source.HDRFacts.Formats[0] != api.HDRFormatSDR {
			return comparisons.MatchResult{}, nil, false, nil
		}
		duration := private.Source.VideoDurationSeconds
		if duration <= 0 && private.Source.StartTimeSeconds == 0 {
			duration = private.Source.DurationSeconds
		}
		if duration < 60 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return comparisons.MatchResult{}, nil, false, nil
		}
		durations[i] = duration
		if source.ID == plan.MainSourceID {
			main = i
		}
	}
	if main < 0 {
		return comparisons.MatchResult{}, nil, false, nil
	}
	duration := durations[main]
	renderer, err := b.comparisonService()
	if err != nil {
		return comparisons.MatchResult{}, nil, false, err
	}
	progress.stages += 3
	type windowKey struct {
		source          int
		start, duration float64
	}
	cache := map[windowKey][]screenshots.ComparisonSample{}
	item, total := 0, 1
	sample := func(source int, center, radius float64) ([]screenshots.ComparisonSample, error) {
		start, end := max(0, center-radius), min(durations[source], center+radius)
		if end-start < 2 {
			return nil, nil
		}
		key := windowKey{source, start, end - start}
		if frames, ok := cache[key]; ok {
			return frames, nil
		}
		id := fmt.Sprintf("%d/%d", source, item)
		frames, err := renderer.SampleComparisonSource(
			progress.child(ctx, item, total, id, "comparison_window", fmt.Sprintf("Column %d", source+1)),
			retained.ComparisonSources[plan.Sources[source].ID].Source,
			start,
			end-start,
			.002,
		)
		if err != nil {
			return nil, fmt.Errorf("decode comparison alignment column %d: %w", source+1, err)
		}
		original := len(frames)
		frames = orderedComparisonSamples(frames)
		if len(frames) != original {
			frames = nil
		}
		cache[key] = frames
		progress.item(item, total, id, "comparison_window", fmt.Sprintf("Column %d", source+1), api.StageStatusCompleted, 1000)
		item++
		return frames, nil
	}
	fallback := func(reason string) (comparisons.MatchResult, map[string][]screenshots.ComparisonSample, bool, error) {
		progress.logger.Infof("comparisons: phase=%s decision=full_scan reason=%s", progress.phase, reason)
		progress.complete()
		progress.stages = progress.stage + 5
		return comparisons.MatchResult{}, nil, false, nil
	}
	offsets := map[string]comparisons.ConstantAlignment{}
	item, total = 0, 3*len(plan.Sources)
	progress.begin("alignment_calibration", total)
	for _, fraction := range []float64{.2, .5, .8} {
		reference, err := sample(main, duration*fraction, 2)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, err
		}
		for i, source := range plan.Sources {
			if i == main {
				continue
			}
			target, err := sample(i, duration*fraction, 32)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, err
			}
			alignment, ok := comparisons.DiscoverAlignment(comparisonSamples(reference), comparisonSamples(target))
			if !ok {
				return fallback("weak_calibration")
			}
			if previous, exists := offsets[source.ID]; exists && !previous.Consistent(alignment) {
				return fallback("contradictory_calibration")
			}
			offsets[source.ID] = alignment
		}
	}
	progress.complete()
	item, total = 0, 2*len(plan.Sources)
	progress.begin("alignment_validation", total)
	for _, fraction := range []float64{.35, .65} {
		reference, err := sample(main, duration*fraction, 2)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, err
		}
		for i, source := range plan.Sources {
			if i == main {
				continue
			}
			expected := offsets[source.ID]
			target, err := sample(i, duration*fraction+expected.OffsetSeconds, 7)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, err
			}
			checked, ok := comparisons.DiscoverAlignment(comparisonSamples(reference), comparisonSamples(target))
			if !ok || !expected.Consistent(checked) {
				return fallback("unverified_offset")
			}
		}
	}
	progress.complete()
	item, total = 0, options.Count*len(plan.Sources)
	progress.begin("alignment_rows", total)
	sampled := make([]comparisons.SampledSource, len(plan.Sources))
	observations := map[string][]screenshots.ComparisonSample{}
	for i, source := range plan.Sources {
		sampled[i] = comparisons.SampledSource{
			ID:            source.ID,
			PreferBFrames: true,
			AllowedTypes:  slices.Clone(source.AllowedFrameTypes),
		}
	}
	for row := range options.Count {
		center := duration * float64(row+1) / float64(options.Count+1)
		reference, err := sample(main, center, 2)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, err
		}
		observations[plan.MainSourceID] = append(observations[plan.MainSourceID], reference...)
		for i, source := range plan.Sources {
			if i == main {
				continue
			}
			// Cover every possible main candidate plus the existing quarter-second
			// picture-type neighborhood. Captures later recheck exact decoded identity.
			target, err := sample(i, center+offsets[source.ID].OffsetSeconds, 2.25)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, err
			}
			observations[source.ID] = append(observations[source.ID], target...)
		}
	}
	for i, source := range plan.Sources {
		observations[source.ID] = orderedComparisonSamples(observations[source.ID])
		if len(observations[source.ID]) == 0 {
			return fallback("empty_rows")
		}
		sampled[i].Frames = comparisonSamples(observations[source.ID])
	}
	result, err := comparisons.AlignedMatch(sampled, plan.MainSourceID, options, offsets)
	if err != nil {
		return comparisons.MatchResult{}, nil, false, fmt.Errorf("match aligned comparison regions: %w", err)
	}
	if len(result.Rows) != options.Count || slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool { return !row.Complete }) {
		return fallback("incomplete_rows")
	}
	progress.complete()
	progress.stages = progress.stage + 1
	progress.logger.Infof("comparisons: phase=alignment_rows decision=constant_offset rows=%d sources=%d", len(result.Rows), len(plan.Sources))
	return result, observations, true, nil
}
