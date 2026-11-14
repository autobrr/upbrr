// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	fallback := func(reason string, column int) {
		phase := progress.phase
		if phase == "" {
			phase = "alignment_eligibility"
		}
		progress.logger.Infof("comparisons: phase=%s decision=full_scan reason=%s column=%d", phase, reason, column)
		if progress.active {
			progress.complete()
		}
		progress.stages = progress.stage + 5
	}
	samplingFailure := func(err error, column int) error {
		if errors.Is(err, screenshots.ErrComparisonNormalizationUnavailable) {
			fallback("normalization_unavailable", column)
			return nil
		}
		if errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
			fallback("fast_budget_exhausted", column)
			return nil
		}
		return err
	}
	main := -1
	matchAspect := 0.0
	durations := make([]float64, len(plan.Sources))
	if options.Count < 1 || options.Count > 16 {
		fallback("row_count", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	if options.DarkCount != 0 || options.MotionCount != 0 {
		fallback("scene_targets", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	if len(options.MainMoments) != 0 || len(plan.Anchors) != 0 {
		fallback("manual_correspondence", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	for i, source := range plan.Sources {
		private, ok := retained.ComparisonSources[source.ID]
		if !ok {
			fallback("missing_source", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		if !source.PreferBFrames {
			fallback("picture_type_policy", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		width, height := private.Source.DisplaySize()
		if width <= 0 || height <= 0 {
			fallback("unsupported_geometry", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		matchAspect = max(matchAspect, float64(width)/float64(height))
		duration := private.Source.VideoDurationSeconds
		if duration <= 0 && private.Source.StartTimeSeconds == 0 {
			duration = private.Source.DurationSeconds
		}
		if duration < 60 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			fallback("unsupported_duration", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		durations[i] = duration
		if source.ID == plan.MainSourceID {
			main = i
		}
	}
	if main < 0 {
		fallback("missing_main_source", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	duration := durations[main]
	renderer, err := b.comparisonService()
	if err != nil {
		return comparisons.MatchResult{}, nil, false, err
	}
	progress.stages = progress.stage + 3
	type windowKey struct {
		source          int
		start, duration float64
	}
	cache := map[windowKey][]screenshots.ComparisonSample{}
	decodedSeconds := make([]float64, len(plan.Sources))
	budgets := make([]screenshots.ComparisonDecodeBudget, len(plan.Sources))
	for i := range budgets {
		budgets[i].RemainingEncodedBytes = 8 << 30
	}
	defer func() {
		for i := range plan.Sources {
			progress.logger.Debugf(
				"comparisons: phase=alignment_budget column=%d requested_seconds=%.6f encoded_span_bytes=%d remaining_seconds=%.6f remaining_span_bytes=%d",
				i+1,
				decodedSeconds[i],
				(8<<30)-budgets[i].RemainingEncodedBytes,
				900-decodedSeconds[i],
				budgets[i].RemainingEncodedBytes,
			)
		}
	}()
	activePictures := map[int]*screenshots.ComparisonCrop{}
	if detector, ok := renderer.(interface {
		DetectComparisonActivePicture(context.Context, screenshots.ComparisonSource, float64, float64, *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error)
	}); ok {
		for i, source := range plan.Sources {
			input := retained.ComparisonSources[source.ID].Source
			if input.Input == nil || input.Input.Kind != "dvd" {
				continue
			}
			crop, err := detector.DetectComparisonActivePicture(ctx, input, durations[i]*.2-2, 4, &budgets[i])
			decodedSeconds[i] += 4
			if err != nil {
				return comparisons.MatchResult{}, nil, false, samplingFailure(err, i+1)
			}
			activePictures[i] = crop
			if crop != nil {
				progress.logger.Debugf("comparisons: phase=alignment_normalization column=%d crop=%dx%d+%d+%d", i+1, crop.Width, crop.Height, crop.X, crop.Y)
			}
		}
	}
	item, total := 0, 1
	sample := func(source int, center, radius float64) ([]screenshots.ComparisonSample, error) {
		start, end := max(0, center-radius), min(durations[source], center+radius)
		windowDuration := min(64, end-start)
		if end-start < 1.1 {
			return nil, nil
		}
		key := windowKey{source, start, windowDuration}
		if frames, ok := cache[key]; ok {
			return frames, nil
		}
		if end-start > 64+1e-9 || decodedSeconds[source]+windowDuration > 900 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		decodedSeconds[source] += windowDuration
		id := fmt.Sprintf("%d/%d", source, item)
		frames, err := renderer.SampleComparisonSource(
			progress.child(ctx, item, total, id, "comparison_window", fmt.Sprintf("Column %d", source+1)),
			screenshots.ComparisonSampleRequest{
				MatchCrop:        activePictures[source],
				Budget:           &budgets[source],
				Source:           retained.ComparisonSources[plan.Sources[source].ID].Source,
				StartSeconds:     start,
				DurationSeconds:  windowDuration,
				StrideSeconds:    .002,
				NormalizeSDR:     true,
				MatchAspectRatio: matchAspect,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("decode comparison alignment column %d: %w", source+1, err)
		}
		original := len(frames)
		frames, err = orderedComparisonSamples(frames)
		if err != nil {
			return nil, err
		}
		if len(frames) != original {
			frames = nil
		}
		cache[key] = frames
		progress.item(item, total, id, "comparison_window", fmt.Sprintf("Column %d", source+1), api.StageStatusCompleted, 1000)
		item++
		return frames, nil
	}
	offsets := map[string]comparisons.ConstantAlignment{}
	hypotheses := map[string][]comparisons.PlaybackScale{}
	proofs := map[string]map[comparisons.PlaybackScale][]comparisons.ConstantAlignment{}
	mainRate := retained.ComparisonSources[plan.MainSourceID].Source
	for i, source := range plan.Sources {
		if i == main {
			continue
		}
		rate := retained.ComparisonSources[source.ID].Source
		hypotheses[source.ID] = comparisons.PlaybackScales(
			mainRate.FrameRateNumerator,
			mainRate.FrameRateDenominator,
			rate.FrameRateNumerator,
			rate.FrameRateDenominator,
		)
		proofs[source.ID] = map[comparisons.PlaybackScale][]comparisons.ConstantAlignment{}
	}
	item, total = 0, 3*len(plan.Sources)
	progress.begin("alignment_calibration", total)
	referenceWindow, err := sample(main, duration*.5, 32)
	if err != nil {
		return comparisons.MatchResult{}, nil, false, samplingFailure(err, main+1)
	}
	centers, reason := comparisons.CalibrationCenters(comparisonSamples(referenceWindow))
	if reason != "" {
		fallback(reason, main+1)
		return comparisons.MatchResult{}, nil, false, nil
	}
	for i, source := range plan.Sources {
		if i == main {
			continue
		}
		target, err := sample(i, durations[i]*.5, 32)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, samplingFailure(err, i+1)
		}
		hypotheses[source.ID] = comparisons.NativePlaybackScales(comparisonSamples(referenceWindow), comparisonSamples(target), hypotheses[source.ID])
		if len(hypotheses[source.ID]) == 0 {
			fallback("rate_hypothesis_limit", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
	}
	for _, center := range centers {
		var reference []screenshots.ComparisonSample
		for _, frame := range referenceWindow {
			if frame.RelativeTimestampSeconds >= center-2 && frame.RelativeTimestampSeconds < center+2 {
				reference = append(reference, frame)
			}
		}
		for i, source := range plan.Sources {
			if i == main {
				continue
			}
			target, err := sample(i, durations[i]*.5, 32)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, samplingFailure(err, i+1)
			}
			for _, scale := range hypotheses[source.ID] {
				alignment, reason := comparisons.DiscoverPlaybackAlignment(comparisonSamples(reference), comparisonSamples(target), scale)
				if reason != "" {
					progress.logger.Debugf(
						"comparisons: phase=alignment_calibration decision=inconclusive column=%d reason=%s scale=%d/%d",
						i+1,
						reason,
						scale.Numerator,
						scale.Denominator,
					)
					continue
				}
				proofs[source.ID][scale] = append(proofs[source.ID][scale], alignment)
				progress.logger.Debugf(
					"comparisons: phase=alignment_calibration decision=region_verified column=%d center_seconds=%.3f scale=%d/%d offset_seconds=%.9f tolerance_seconds=%.9f",
					i+1,
					center,
					scale.Numerator,
					scale.Denominator,
					alignment.OffsetSeconds,
					alignment.ToleranceSeconds,
				)
			}
		}
	}
	for id, proposals := range proofs {
		var accepted []comparisons.ConstantAlignment
		for scale, regions := range proposals {
			if scale.Numerator == scale.Denominator && len(hypotheses[id]) == 1 && len(regions) > 0 && !regions[0].IntervalProof {
				accepted = append(accepted, regions[0])
				continue
			}
			if fit, verified := comparisons.VerifyPlaybackCalibration(regions); verified {
				accepted = append(accepted, fit)
			}
		}
		if len(accepted) == 1 {
			offsets[id] = accepted[0]
			progress.logger.Infof(
				"comparisons: phase=alignment_calibration decision=verified source=%s scale=%d/%d offset_seconds=%.9f",
				id,
				accepted[0].ScaleNumerator,
				accepted[0].ScaleDenominator,
				accepted[0].OffsetSeconds,
			)
		}
	}
	for i, source := range plan.Sources {
		if _, found := offsets[source.ID]; i != main && !found {
			fallback("calibration_exhausted", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
	}
	progress.complete()
	item, total = 0, options.Count*len(plan.Sources)
	progress.begin("alignment_rows", total)
	observations := map[string][]screenshots.ComparisonSample{}
	result := comparisons.MatchResult{}
	spacing := duration / float64(options.Count+1)
	radius := min(2, spacing/4)
	shifts := []float64{
		0, min(8, spacing/8), -min(8, spacing/8), min(24, spacing/4), -min(24, spacing/4),
		min(60, spacing/3), -min(60, spacing/3), min(120, spacing*.4), -min(120, spacing*.4),
	}
	for row := range options.Count {
		accepted := false
		lastColumn := 0
		for _, shift := range shifts {
			lastColumn = 0
			item = row * len(plan.Sources)
			center := spacing*float64(row+1) + shift
			reference, err := sample(main, center, radius)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, samplingFailure(err, main+1)
			}
			sampled := make([]comparisons.SampledSource, len(plan.Sources))
			local := map[string]comparisons.ConstantAlignment{}
			frames := map[string][]screenshots.ComparisonSample{plan.MainSourceID: reference}
			for i, source := range plan.Sources {
				sampled[i] = comparisons.SampledSource{
					ID:            source.ID,
					PreferBFrames: true,
					AllowedTypes:  slices.Clone(source.AllowedFrameTypes),
				}
				if i == main {
					sampled[i].Frames = comparisonSamples(reference)
					continue
				}
				// Calibration predicts only a bounded search region. Each row gets
				// independent temporal proof, so small timing edits need no full scan.
				target, err := sample(i, offsets[source.ID].Predict(center), radius+4)
				if err != nil {
					return comparisons.MatchResult{}, nil, false, samplingFailure(err, i+1)
				}
				prior := offsets[source.ID]
				alignment, reason := comparisons.DiscoverPlaybackAlignment(
					comparisonSamples(reference),
					comparisonSamples(target),
					comparisons.PlaybackScale{Numerator: prior.ScaleNumerator, Denominator: prior.ScaleDenominator},
				)
				if reason != "" {
					lastColumn = i + 1
					progress.logger.Debugf(
						"comparisons: phase=alignment_rows decision=retry row=%d column=%d reason=%s center_seconds=%.3f",
						row+1,
						i+1,
						reason,
						center,
					)
					break
				}
				local[source.ID] = alignment
				frames[source.ID] = target
				sampled[i].Frames = comparisonSamples(target)
			}
			if len(local) != len(plan.Sources)-1 {
				continue
			}
			matched, err := comparisons.AlignedMatch(sampled, plan.MainSourceID, comparisons.MatchOptions{Count: 1}, local, result.Rows)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, fmt.Errorf("match bounded comparison row: %w", err)
			}
			if len(matched.Rows) != 1 || !matched.Rows[0].Complete {
				if len(matched.Rows) == 1 {
					for i, frame := range matched.Rows[0].Frames {
						if !frame.Resolved {
							lastColumn = i + 1
							break
						}
					}
				}
				progress.logger.Debugf("comparisons: phase=alignment_rows decision=retry row=%d reason=incomplete_row center_seconds=%.3f", row+1, center)
				continue
			}
			result.Rows = append(result.Rows, matched.Rows...)
			result.AchievedDark += matched.AchievedDark
			result.AchievedMotion += matched.AchievedMotion
			for id, decoded := range frames {
				observations[id] = append(observations[id], decoded...)
			}
			maps.Copy(offsets, local)
			accepted = true
			break
		}
		if !accepted {
			fallback("row_alignment_exhausted", lastColumn)
			return comparisons.MatchResult{}, nil, false, nil
		}
		progress.update(row+1, options.Count)
	}
	for id, frames := range observations {
		observations[id], err = orderedComparisonSamples(frames)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, err
		}
	}
	progress.complete()
	progress.stages = progress.stage + 1
	progress.logger.Infof("comparisons: phase=alignment_rows decision=bounded_local rows=%d sources=%d", len(result.Rows), len(plan.Sources))
	return result, observations, true, nil
}
