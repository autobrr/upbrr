// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// tryAlignedComparisons owns bounded decoding, not matching policy. Retained
// calibration only predicts search windows; every row needs fresh native proof.
// Decoded windows live only for this attempt.
func (b workflowMediaBuilder) tryAlignedComparisons(ctx context.Context, retained *workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan, options comparisons.MatchOptions, progress *comparisonProgress,
) (returned comparisons.MatchResult, returnedObservations map[string][]screenshots.ComparisonSample, accepted bool, returnedErr error) {
	lastReason := ""
	noteInconclusive := func(reason string, column int) {
		lastReason = reason
		phase := progress.phase
		if phase == "" {
			phase = "alignment_eligibility"
		}
		progress.logger.Infof("comparisons: phase=%s decision=inconclusive reason=%s column=%d", phase, reason, column)
		if progress.active {
			progress.complete()
		}
		progress.stages = progress.stage + 5
	}
	samplingFailure := func(err error, column int) error {
		if errors.Is(err, screenshots.ErrComparisonNormalizationUnavailable) {
			noteInconclusive("normalization_unavailable", column)
			return nil
		}
		if errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
			noteInconclusive("fast_budget_exhausted", column)
			return nil
		}
		return err
	}
	main := -1
	matchAspect := comparisonMatchAspect(*retained, plan)
	durations := make([]float64, len(plan.Sources))
	if options.Count < 1 || options.Count > 16 {
		noteInconclusive("row_count", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	for i, source := range plan.Sources {
		private, ok := retained.ComparisonSources[source.ID]
		if !ok {
			noteInconclusive("missing_source", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		width, height := private.Source.DisplaySize()
		if width <= 0 || height <= 0 {
			noteInconclusive("unsupported_geometry", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		duration := private.Source.VideoDurationSeconds
		if duration <= 0 && private.Source.StartTimeSeconds == 0 {
			duration = private.Source.DurationSeconds
		}
		if duration < 4 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			noteInconclusive("unsupported_duration", i+1)
			return comparisons.MatchResult{}, nil, false, nil
		}
		durations[i] = duration
		if source.ID == plan.MainSourceID {
			main = i
		}
	}
	if main < 0 {
		noteInconclusive("missing_main_source", 0)
		return comparisons.MatchResult{}, nil, false, nil
	}
	duration := durations[main]
	renderer, err := b.comparisonService()
	if err != nil {
		return comparisons.MatchResult{}, nil, false, err
	}
	if inspector, ok := renderer.(interface {
		ComparisonAnalysisExecutableIdentity(context.Context) (string, error)
	}); ok {
		identity, err := inspector.ComparisonAnalysisExecutableIdentity(ctx)
		if err != nil {
			return comparisons.MatchResult{}, nil, false, fmt.Errorf("identify comparison analysis executable: %w", err)
		}
		retained.ComparisonAnalysisExecutable = identity
	}
	progress.stages = progress.stage + 4
	if plan.Preferences.ScanMode != "deep" {
		// Quick Scan can append discovery windows while matching.
		// Reserve their phases now so capture retains the final progress unit.
		progress.stages += comparisonQuickWindows
	}
	type windowKey struct {
		source          int
		start, duration float64
	}
	cache := map[windowKey][]screenshots.ComparisonSample{}
	secondaryCache := comparisonWindowCache{}
	var mainInventory *comparisonSceneInventory
	var inventoryOwner string
	analysisBytes := int64(0)
	retainedBytes := int64(0)
	candidateBytes := int64(0)
	renderers := make([]screenshots.ComparisonAnalysisRenderer, len(plan.Sources))
	decodedSeconds := make([]float64, len(plan.Sources))
	sampleChunkSeconds := make([]float64, len(plan.Sources))
	budgets := make([]screenshots.ComparisonDecodeBudget, len(plan.Sources))
	aggregateBytes := int64(24 << 30)
	for i := range budgets {
		sampleChunkSeconds[i] = 8
		budgets[i].RemainingEncodedBytes = 8 << 30
		budgets[i].AttemptRemaining = &aggregateBytes
	}
	defer func() {
		for i := range plan.Sources {
			progress.logger.Debugf(
				"comparisons: phase=alignment_budget column=%d requested_seconds=%.6f encoded_span_bytes=%d remaining_seconds=%.6f remaining_span_bytes=%d",
				i+1,
				decodedSeconds[i],
				(8<<30)-budgets[i].RemainingEncodedBytes,
				comparisonAutomaticDecodeLimit(options.Count)-decodedSeconds[i],
				budgets[i].RemainingEncodedBytes,
			)
		}
	}()
	activePictures := map[int]*screenshots.ComparisonCrop{}
	offsets := map[string]comparisons.ConstantAlignment{}
	calibrationProfiles := make([]string, len(plan.Sources))
	detector, canDetectPicture := renderer.(interface {
		DetectComparisonActivePicture(context.Context, screenshots.ComparisonSource, float64, float64, *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error)
	})
	detectPicture := func(workCtx context.Context, source int) error {
		if !canDetectPicture {
			return nil
		}
		input := retained.ComparisonSources[plan.Sources[source].ID].Source
		start := max(0, durations[source]*.2-2)
		seconds := min(4, durations[source]-start)
		crop, err := detector.DetectComparisonActivePicture(workCtx, input, start, seconds, &budgets[source])
		decodedSeconds[source] += seconds
		if err != nil {
			return samplingFailure(err, source+1)
		}
		activePictures[source] = crop
		if crop != nil {
			progress.logger.Debugf("comparisons: phase=alignment_normalization column=%d crop=%dx%d+%d+%d", source+1, crop.Width, crop.Height, crop.X, crop.Y)
		}
		return nil
	}
	for i := range plan.Sources {
		if i != main {
			id := plan.Sources[i].ID
			profile, err := b.comparisonCalibrationProfile(*retained, plan, id)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, fmt.Errorf("identify comparison calibration: %w", err)
			}
			calibrationProfiles[i] = profile
			private := retained.ComparisonSources[id]
			if prediction, ok := private.Calibration.prediction(profile, private.Source); ok {
				offsets[id] = prediction
				calibration := private.Calibration.clone()
				activePictures[i] = calibration.Crop
				progress.logger.Debugf("comparisons: phase=alignment_calibration decision=reused column=%d", i+1)
				continue
			}
			private.Calibration = nil
			retained.ComparisonSources[id] = private
			if err := detectPicture(ctx, i); err != nil {
				return comparisons.MatchResult{}, nil, false, err
			}
		}
	}
	item, total := 0, 1
	matchedRows := 0
	matchingRows := false
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
		if source == main && mainInventory != nil {
			var frames []screenshots.ComparisonSample
			full := false
			if err := readComparisonInventoryRange(
				ctx,
				mainInventory,
				inventoryOwner,
				&api.ComparisonTimeInterval{Start: start, End: end},
				func(frame screenshots.ComparisonSample) bool {
					if frame.RelativeTimestampSeconds >= start && frame.RelativeTimestampSeconds < end {
						if candidateBytes+retainedBytes+analysisBytes+secondaryCache.bytes+int64(len(frames)+1)*4096 > (112 << 20) {
							full = true
							return false
						}
						frames = append(frames, frame)
					}
					return true
				},
			); err != nil {
				return nil, err
			}
			if full {
				return nil, screenshots.ErrComparisonFastInconclusive
			}
			retainedBytes += int64(len(frames)) * 4096
			cache[key] = frames
			return frames, nil
		}
		if end-start > 64+1e-9 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		id := fmt.Sprintf("%d/%d", source, item)
		var frames []screenshots.ComparisonSample
		reusedWindow := false
		missing := []api.ComparisonTimeInterval{{Start: start, End: end}}
		if matchingRows && source != main {
			secondaryCache.trim(min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes))
			frames, missing = secondaryCache.get(source, start, end, renderers[source].Mode)
			reusedWindow = len(frames) > 0
			retainedBytes += int64(len(frames)) * 4096
			missingSeconds := 0.0
			for _, gap := range missing {
				missingSeconds += gap.End - gap.Start
			}
			if missingSeconds < windowDuration {
				progress.logger.Tracef("comparisons: phase=alignment_cache decision=reused column=%d seconds=%.6f frames=%d",
					source+1, windowDuration-missingSeconds, len(frames))
			}
		}
		for _, gap := range missing {
			if decodedSeconds[source]+gap.End-gap.Start > comparisonAutomaticDecodeLimit(options.Count) {
				return nil, screenshots.ErrComparisonFastInconclusive
			}
			decodedSeconds[source] += gap.End - gap.Start
			for chunkStart := gap.Start; chunkStart < gap.End-1e-9; {
				secondaryCache.trim(min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes))
				available := (128 << 20) - candidateBytes - retainedBytes - analysisBytes - secondaryCache.bytes - (16 << 20)
				if available < 4096 {
					return nil, screenshots.ErrComparisonFastInconclusive
				}
				chunkDuration := min(sampleChunkSeconds[source], gap.End-chunkStart)
				request := screenshots.ComparisonSampleRequest{
					RetentionLimitBytes: available,
					MatchCrop:           activePictures[source],
					Budget:              &budgets[source],
					Renderer:            &renderers[source],
					Source:              retained.ComparisonSources[plan.Sources[source].ID].Source,
					StartSeconds:        chunkStart,
					DurationSeconds:     chunkDuration,
					StrideSeconds:       .002,
					NativeFrames:        true,
					NormalizeSDR:        true,
					MatchAspectRatio:    matchAspect,
				}
				workIndex, workTotal := item, total
				if matchingRows {
					workIndex, workTotal = min(matchedRows, options.Count-1)*len(plan.Sources)+source, options.Count*len(plan.Sources)
				}
				workCtx := progress.child(ctx, workIndex, workTotal, id, "comparison_window", fmt.Sprintf("Column %d", source+1))
				started := time.Now()
				decoded, err := renderer.SampleComparisonSource(workCtx, request)

				if err != nil {
					return nil, fmt.Errorf("decode comparison alignment column %d: %w", source+1, err)
				}
				if matchingRows {
					// Keep shared native boundary pictures for identity checking;
					// trim only outside the complete requested window.
					decoded = slices.DeleteFunc(decoded, func(frame screenshots.ComparisonSample) bool {
						return frame.RelativeTimestampSeconds < start || frame.RelativeTimestampSeconds >= end
					})
				}
				sampleChunkSeconds[source] = comparisonSampleChunkSeconds(sampleChunkSeconds[source], chunkDuration, time.Since(started))
				retainedBytes += int64(len(decoded)) * 4096
				if candidateBytes+retainedBytes+analysisBytes+secondaryCache.bytes > 128<<20 {
					return nil, screenshots.ErrComparisonFastInconclusive
				}
				frames = append(frames, decoded...)
				chunkStart += chunkDuration
			}
		}
		original := len(frames)
		frames, orderErr := orderedComparisonSamples(frames)
		if orderErr != nil {
			return nil, orderErr
		}
		if len(frames) != original && !reusedWindow {
			frames = nil
		}
		cache[key] = frames
		if matchingRows && source != main {
			secondaryCache.put(source, start, end, renderers[source].Mode, frames,
				min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes))
		}
		workIndex, workTotal := item, total
		if matchingRows {
			workIndex, workTotal = min(matchedRows, options.Count-1)*len(plan.Sources)+source, options.Count*len(plan.Sources)
		}
		progress.item(workIndex, workTotal, id, "comparison_window", fmt.Sprintf("Column %d", source+1), api.StageStatusCompleted, 1000)
		item++
		return frames, nil
	}
	window, excluded, err := comparisons.SelectionRange(plan.Preferences, duration, comparisonTelevision(*retained))
	if err != nil {
		return comparisons.MatchResult{}, nil, false, fmt.Errorf("comparison scene analysis: %w", err)
	}
	analyzer, canAnalyze := renderer.(interface {
		AnalyzeComparisonSource(context.Context, screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error)
	})
	if !canAnalyze {
		return comparisons.MatchResult{}, nil, false, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			"analysis_unavailable",
			"This decoder cannot provide verified comparison scene analysis.",
		)
	}
	discoveryCtx := ctx
	var stopDiscovery context.CancelFunc = func() {}
	if plan.Preferences.ScanMode == "deep" {
		start, _ := ctx.Value(comparisonOperationStartKey{}).(time.Time)
		if start.IsZero() {
			start = time.Now()
		}
		discoveryCtx, stopDiscovery = context.WithDeadline(ctx, start.Add(comparisonDiscoveryAllowance(duration)))
	}
	mainPictureReady := false
	discoveryDecode := func(workCtx context.Context, start, seconds float64, deep bool) ([]screenshots.ComparisonSample, error) {
		// A compatible complete Deep inventory already contains normalized main
		// pixels. Probe the main crop only when fresh analysis actually starts.
		if !mainPictureReady {
			if err := detectPicture(workCtx, main); err != nil {
				return nil, err
			}
			mainPictureReady = true
		}
		secondaryCache.trim(min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes))
		available := (128 << 20) - candidateBytes - retainedBytes - analysisBytes - secondaryCache.bytes - (16 << 20)
		if available < 4096 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		budget := &budgets[main]
		if deep {
			budget = &screenshots.ComparisonDecodeBudget{RemainingEncodedBytes: 1 << 62}
		} else {
			if decodedSeconds[main]+seconds > comparisonAutomaticDecodeLimit(options.Count) {
				return nil, screenshots.ErrComparisonFastInconclusive
			}
			decodedSeconds[main] += seconds
		}
		frames, err := analyzer.AnalyzeComparisonSource(workCtx, screenshots.ComparisonSampleRequest{
			RetentionLimitBytes: available,
			MatchCrop:           activePictures[main],
			Source:              retained.ComparisonSources[plan.MainSourceID].Source,
			StartSeconds:        start,
			DurationSeconds:     seconds,
			StrideSeconds:       .002,
			NativeFrames:        true,
			NormalizeSDR:        true,
			MatchAspectRatio:    matchAspect,
			Budget:              budget,
			Renderer:            &renderers[main],
		})
		if err != nil {
			return frames, fmt.Errorf("analyze main comparison section: %w", err)
		}
		return frames, nil
	}
	inventory, discoveryErr := b.discoverComparisonScenes(discoveryCtx, retained, plan, window, excluded, discoveryDecode, progress)
	stopDiscovery()
	if discoveryErr != nil {
		return comparisons.MatchResult{}, nil, false, discoveryErr
	}
	if plan.Preferences.ScanMode == "deep" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(comparisonMatchingAllowance(options.Count, len(plan.Sources)))*time.Second)
		defer cancel()
	}
	owner, err := b.comparisonInventoryOwner(*retained, plan)
	if err != nil {
		return comparisons.MatchResult{}, nil, false, err
	}
	if plan.Preferences.ScanMode == "deep" {
		mainInventory, inventoryOwner = inventory, owner
		if !inventory.Summary.Complete || !inventory.Summary.RankingComplete {
			partial := comparisons.MatchResult{
				RequestedDark:   options.DarkCount,
				RequestedMotion: options.MotionCount,
				RequestedFlat:   options.FlatCount,
				Analysis:        &inventory.Summary,
			}
			if err := ctx.Err(); err != nil {
				return partial, nil, true, fmt.Errorf("finish main-input Deep discovery: %w", err)
			}
			return partial, nil, true, &releaseworkflow.ComparisonIncompleteError{
				Cause:  errors.New("main-input Deep discovery did not finish"),
				Reason: "discovery_incomplete",
			}
		}
	}
	// Reserve half of the shared retention allowance for native windows. Larger
	// ranking inventories use the full streaming selector without truncation.
	candidates, candidateBytes, err := comparisonInventoryCandidates(ctx, inventory, owner, (64<<20)-comparisonInventoryIndexBytes(inventory))
	candidateBytes += comparisonInventoryIndexBytes(inventory)
	if err != nil {
		return comparisons.MatchResult{}, nil, false, err
	}
	ranking := "indexed"
	if candidates == nil {
		ranking = "streaming"
	}
	progress.logger.Debugf("comparisons: phase=main_ranking decision=%s indexed_frames=%d retained_bytes=%d", ranking, len(candidates), candidateBytes)
	unavailable := func() comparisons.MatchResult {
		if lastReason == "" {
			lastReason = "bounded_matching_inconclusive"
		}
		analysis := inventory.Summary
		analysis.Reasons = append(slices.Clone(analysis.Reasons), lastReason)
		return comparisons.MatchResult{
			RequestedDark:   options.DarkCount,
			RequestedMotion: options.MotionCount,
			RequestedFlat:   options.FlatCount,
			Analysis:        &analysis,
			Warnings: []string{
				"Correspondence could not be established within bounded work. Inspect manual correspondences or revise the selection range.",
			},
		}
	}
	failed := func(err error, column int) (comparisons.MatchResult, map[string][]screenshots.ComparisonSample, bool, error) {
		if failure := samplingFailure(err, column); failure != nil {
			return comparisons.MatchResult{}, nil, false, failure
		}
		return unavailable(), nil, true, nil
	}
	hypotheses := map[string][]comparisons.PlaybackScale{}
	proofs := map[string]map[comparisons.PlaybackScale][]comparisons.ConstantAlignment{}
	mainRate := retained.ComparisonSources[plan.MainSourceID].Source
	for i, source := range plan.Sources {
		if _, reused := offsets[source.ID]; i == main || reused {
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
	if len(offsets) < len(plan.Sources)-1 {
		referenceWindow, err := sample(main, duration*.5, 32)
		if err != nil {
			return failed(err, main+1)
		}
		centers, reason := comparisons.CalibrationCenters(comparisonSamples(referenceWindow))
		if reason != "" {
			if duration <= 32 {
				centers = []float64{duration * .5}
			} else {
				noteInconclusive(reason, main+1)
				return unavailable(), nil, true, nil
			}
		}
		for i, source := range plan.Sources {
			if _, reused := offsets[source.ID]; i == main || reused {
				continue
			}
			target, err := sample(i, durations[i]*.5, 32)
			if err != nil {
				return failed(err, i+1)
			}
			hypotheses[source.ID] = comparisons.NativePlaybackScales(comparisonSamples(referenceWindow), comparisonSamples(target), hypotheses[source.ID])
			if len(hypotheses[source.ID]) == 0 {
				noteInconclusive("rate_hypothesis_limit", i+1)
				return unavailable(), nil, true, nil
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
				if _, reused := offsets[source.ID]; i == main || reused {
					continue
				}
				target, err := sample(i, durations[i]*.5, 32)
				if err != nil {
					return failed(err, i+1)
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
		// Native region proofs own their checked points, not the decoded rasters.
		// Release those windows before admitting any additional calibration region.
		clear(cache)
		retainedBytes = 0
		calibrationHypotheses := func(id string) []comparisons.ConstantAlignment {
			var accepted []comparisons.ConstantAlignment
			for scale, regions := range proofs[id] {
				if scale.Numerator == scale.Denominator && len(hypotheses[id]) == 1 && len(regions) > 0 && !regions[0].IntervalProof {
					accepted = append(accepted, regions[0])
					continue
				}
				if fit, verified := comparisons.VerifyPlaybackCalibration(regions); verified {
					accepted = append(accepted, fit)
				} else if prediction, verified := comparisons.PlaybackSearchPrediction(regions, 2); verified {
					accepted = append(accepted, prediction)
				}
			}
			return accepted
		}
		// An ambiguous region is not a failed held-out proof. When fewer than
		// three regions established correspondence, try regions from at most two
		// additional main windows. Existing fits only locate bounded
		// target windows; every new region must pass the same native proof, and
		// the final three-region fit/held-out validation remains unchanged.
		for i, source := range plan.Sources {
			if _, reused := offsets[source.ID]; i == main || reused || len(calibrationHypotheses(source.ID)) > 0 {
				continue
			}
			for _, scale := range hypotheses[source.ID] {
				regions := proofs[source.ID][scale]
				if len(regions) == 0 || len(regions) >= 3 {
					continue
				}
				attemptedCenters := slices.Clone(centers)
				for _, fraction := range []float64{.35, .65} {
					mainWindow, err := sample(main, duration*fraction, 32)
					if err != nil {
						return failed(err, main+1)
					}
					additional, reason := comparisons.CalibrationCenters(comparisonSamples(mainWindow))
					if reason != "" {
						continue
					}
					for _, center := range additional {
						if slices.ContainsFunc(attemptedCenters, func(attempted float64) bool { return math.Abs(attempted-center) < 12 }) {
							continue
						}
						attemptedCenters = append(attemptedCenters, center)
						var reference []screenshots.ComparisonSample
						for _, frame := range mainWindow {
							if frame.RelativeTimestampSeconds >= center-2 && frame.RelativeTimestampSeconds < center+2 {
								reference = append(reference, frame)
							}
						}
						prediction := regions[0]
						target, err := sample(i, prediction.Predict(center), max(4, 2*prediction.Scale()+2))
						if err != nil {
							return failed(err, i+1)
						}
						alignment, reason := comparisons.DiscoverPlaybackAlignment(comparisonSamples(reference), comparisonSamples(target), scale)
						progress.logger.Tracef(
							"comparisons: phase=alignment_calibration decision=additional_region column=%d center_seconds=%.3f scale=%d/%d reason=%s",
							i+1,
							center,
							scale.Numerator,
							scale.Denominator,
							reason,
						)
						if reason == "" {
							regions = append(regions, alignment)
							proofs[source.ID][scale] = regions
							if len(regions) == 3 {
								break
							}
						}
					}
					if len(regions) == 3 {
						break
					}
				}
			}
		}
		for id := range proofs {
			if prediction, unique := comparisons.SelectCalibrationHypothesis(calibrationHypotheses(id), duration); unique {
				offsets[id] = prediction
				progress.logger.Infof(
					"comparisons: phase=alignment_calibration decision=verified source=%s scale=%d/%d offset_seconds=%.9f",
					id,
					prediction.ScaleNumerator,
					prediction.ScaleDenominator,
					prediction.OffsetSeconds,
				)
			}
		}
	}
	for i, source := range plan.Sources {
		if _, found := offsets[source.ID]; i != main && !found {
			noteInconclusive("calibration_exhausted", i+1)
			return unavailable(), nil, true, nil
		}
		if prediction, found := offsets[source.ID]; i != main && found && calibrationProfiles[i] != "" && renderers[i].Mode != "" {
			private := retained.ComparisonSources[source.ID]
			private.Calibration = (&comparisonCalibration{
				Profile:       calibrationProfiles[i],
				Scale:         comparisons.PlaybackScale{Numerator: prediction.ScaleNumerator, Denominator: prediction.ScaleDenominator},
				OffsetSeconds: prediction.OffsetSeconds,
				Crop:          activePictures[i],
			}).clone()
			retained.ComparisonSources[source.ID] = private
		}
	}
	progress.complete()
	item, total = 0, options.Count*len(plan.Sources)
	progress.begin("alignment_rows", total)
	matchingRows = true
	observations := map[string][]screenshots.ComparisonSample{}
	result := comparisons.MatchResult{
		RequestedDark:   options.DarkCount,
		RequestedMotion: options.MotionCount,
		RequestedFlat:   options.FlatCount,
		Analysis:        &inventory.Summary,
	}
	targets := [3]int{options.MotionCount, options.DarkCount, options.FlatCount}
	schedule := comparisonSceneSlots(targets, options.Count)
	slot, slotAttempts := 0, 0
	used := map[string]bool{}
	var attemptedSections []api.ComparisonTimeInterval
	type matchedSection struct {
		match  comparisons.MatchResult
		frames map[string][]screenshots.ComparisonSample
	}
	commit := func(section matchedSection) {
		result.Rows = append(result.Rows, section.match.Rows...)
		matchedRows = len(result.Rows)
		result.AchievedDark += section.match.AchievedDark
		result.AchievedMotion += section.match.AchievedMotion
		result.AchievedFlat += section.match.AchievedFlat
		for id, decoded := range section.frames {
			observations[id] = append(observations[id], decoded...)
		}
		slot++
		slotAttempts = 0
		progress.item(
			len(result.Rows)-1,
			options.Count,
			strconv.Itoa(len(result.Rows)),
			"comparison_row",
			fmt.Sprintf("Row %d", len(result.Rows)),
			api.StageStatusCompleted,
			1000,
		)
	}
	// Calibration rasters are no longer needed once their checked predictors exist.
	clear(cache)
	retainedBytes = 0
	separation := (window.End - window.Start) / float64(max(1, options.Count*3))
	// Classified decoder stops preserve independently verified rows. Identity and
	// source failures remain closed; publication revalidates retained bindings.
	defer func() {
		workerStopped := len(result.Rows) > 0 && (errors.Is(returnedErr, screenshots.ErrComparisonWorkerFailed) ||
			errors.Is(returnedErr, screenshots.ErrComparisonNormalizationUnavailable))
		if workerStopped {
			returnedErr = &releaseworkflow.ComparisonIncompleteError{Cause: returnedErr, Reason: "match_worker_failed"}
		}
		if workerStopped || errors.Is(returnedErr, context.Canceled) || errors.Is(returnedErr, context.DeadlineExceeded) {
			returned, returnedObservations, accepted = result, observations, true
		}
	}()
search:
	for attempt := 0; attempt < options.Count*30 && slot < len(schedule) && len(result.Rows) < options.Count; attempt++ {
		clear(cache)
		retainedBytes, analysisBytes = 0, 0
		for id, decoded := range observations {
			observations[id] = slices.Clone(slices.DeleteFunc(decoded, func(frame screenshots.ComparisonSample) bool {
				return !slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool {
					return slices.ContainsFunc(row.Frames, func(chosen comparisons.FrameMatch) bool {
						return chosen.SourceID == id && chosen.FrameID == comparisonObservationID(frame.ComparisonFrameObservation)
					})
				})
			}))
			retainedBytes += int64(len(observations[id])) * 4096
		}
		limit := 10
		if schedule[slot] < 0 {
			limit = 20
		}
		if slotAttempts == limit {
			if schedule[slot] >= 0 {
				// Give other requested slots their first pass before a failed
				// quality preference gets one general native retry allowance.
				schedule = append(schedule, -1)
			}
			slot++
			slotAttempts = 0
			continue
		}
		slotAttempts++
		searchTargets := [3]int{}
		if schedule[slot] >= 0 {
			searchTargets[schedule[slot]] = 1
		}
		var inventoryErr error
		sequence := func(yield func(comparisons.FrameSample) bool) {
			visit := func(frame comparisons.FrameSample) bool {
				if !comparisons.EligibleTime(frame.TimestampSeconds, window, excluded) ||
					slices.ContainsFunc(attemptedSections, func(span api.ComparisonTimeInterval) bool {
						return frame.TimestampSeconds >= span.Start && frame.TimestampSeconds <= span.End
					}) {
					return true
				}
				return yield(frame)
			}
			if candidates != nil {
				for _, frame := range candidates {
					if !visit(frame.sample()) {
						break
					}
				}
				return
			}
			inventoryErr = readComparisonInventory(ctx, inventory, owner, func(frame screenshots.ComparisonSample) bool {
				return visit(comparisonSamples([]screenshots.ComparisonSample{frame})[0])
			})
		}
		candidate, category, found := comparisons.SelectScene(
			sequence,
			used,
			result.Rows,
			searchTargets,
			[3]int{},
			searchTargets,
			separation,
		)
		if inventoryErr != nil {
			return comparisons.MatchResult{}, nil, false, inventoryErr
		}
		if !found {
			if !inventory.Summary.RankingComplete {
				lastReason = "main_discovery_incomplete"
				break
			}
			if plan.Preferences.ScanMode != "deep" && inventory.QuickWindows < comparisonQuickWindows &&
				decodedSeconds[main]+4 <= comparisonAutomaticDecodeLimit(options.Count) {
				candidates, candidateBytes = nil, 0
				expanded, err := b.discoverComparisonScenes(ctx, retained, plan, window, excluded, discoveryDecode, progress, true)
				if err != nil {
					return comparisons.MatchResult{}, nil, false, err
				}
				if expanded.QuickWindows > inventory.QuickWindows {
					inventory = expanded
					candidates, candidateBytes, err = comparisonInventoryCandidates(ctx, inventory, owner, (64<<20)-comparisonInventoryIndexBytes(inventory))
					candidateBytes += comparisonInventoryIndexBytes(inventory)
					if err != nil {
						return comparisons.MatchResult{}, nil, false, err
					}
					result.Analysis = &inventory.Summary
					progress.begin("alignment_rows", options.Count*len(plan.Sources))
					continue
				}
			}
			break
		}
		used[candidate.FrameID] = true
		center := candidate.TimestampSeconds
		var reference []screenshots.ComparisonSample
		var quickAnchor screenshots.ComparisonSample
		if mainInventory != nil {
			radius := comparisonSectionSeconds / 2
			if category < 0 {
				radius = 2
			}
			reference, err = sample(main, center, radius)
		} else {
			var original screenshots.ComparisonSample
			if err := readComparisonInventory(ctx, inventory, owner, func(frame screenshots.ComparisonSample) bool {
				if comparisonObservationID(frame.ComparisonFrameObservation) == candidate.FrameID {
					original = frame
					return false
				}
				return true
			}); err != nil {
				return comparisons.MatchResult{}, nil, false, err
			}
			if original.Replay == nil {
				return comparisons.MatchResult{}, nil, false, errors.New("selected comparison anchor lost its original replay")
			}
			quickAnchor = original
			// Quick discovery already computed real evidence and native pixels
			// for these pictures. Keep all of them instead of throwing away the
			// native alternatives between sparse spatial measurements.
			err = readComparisonInventoryRange(ctx, inventory, owner,
				&api.ComparisonTimeInterval{Start: center - 2, End: center + 2},
				func(frame screenshots.ComparisonSample) bool {
					reference = append(reference, frame)
					return true
				})
			if err == nil {
				reference, err = orderedComparisonSamples(reference)
			}
		}
		if err != nil {
			if errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
				break
			}
			return comparisons.MatchResult{}, nil, false, err
		}
		if category >= 0 {
			reference = comparisonEligibleSection(reference, candidate.FrameID)
		}
		if len(reference) < 4 {
			attemptedSections = append(attemptedSections, api.ComparisonTimeInterval{Start: center - 4, End: center + 4})
			continue
		}
		for _, frame := range reference {
			if frame.FrameID == candidate.FrameID {
				candidate = comparisonSamples([]screenshots.ComparisonSample{frame})[0]
				break
			}
		}
		span := api.ComparisonTimeInterval{Start: reference[0].RelativeTimestampSeconds, End: reference[len(reference)-1].RelativeTimestampSeconds}
		attemptedSections = append(attemptedSections, span)
		var matched comparisons.MatchResult
		var selectedFrames map[string][]screenshots.ComparisonSample
		widths := []float64{4, 12, comparisonSectionSeconds}
		if category < 0 {
			widths = widths[:1]
		} else if plan.Preferences.ScanMode == "deep" {
			// Complete main analysis defines this entire observed scene. Search
			// its score band for the best permitted type combination in one pass.
			center = (span.Start + span.End) / 2
			widths = []float64{min(comparisonSectionSeconds, max(4, span.End-span.Start+.1))}
		}
		for _, width := range widths {
			if mainInventory == nil && category >= 0 && width == 12 {
				// Only a failed compact native match pays for wider scene scoring.
				inventory = cloneComparisonInventory(inventory)
				retained.ComparisonInventory, result.Analysis = inventory, &inventory.Summary
				reference, err = scoreComparisonSection(ctx, inventory, quickAnchor,
					func(workCtx context.Context, start, seconds float64) ([]screenshots.ComparisonSample, error) {
						frames, err := discoveryDecode(workCtx, start, seconds, false)
						analysisBytes += int64(len(frames)) * 4096
						return frames, err
					})
				if err != nil {
					if errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
						lastReason = "native_decode_budget_exhausted"
						break search
					}
					return result, observations, true, err
				}
				reference = comparisonEligibleSection(reference, candidate.FrameID)
				if len(reference) < 4 {
					break
				}
				attemptedSections = append(attemptedSections, api.ComparisonTimeInterval{
					Start: reference[0].RelativeTimestampSeconds, End: reference[len(reference)-1].RelativeTimestampSeconds})
			}

			var localReference []screenshots.ComparisonSample
			for _, frame := range reference {
				if frame.RelativeTimestampSeconds >= center-width/2 && frame.RelativeTimestampSeconds < center+width/2 {
					localReference = append(localReference, frame)
				}
			}
			if len(localReference) < 4 {
				continue
			}
			// I-only regions need substantial observed coverage. A single nearby
			// nine-second decode also searches for quick B/P alternatives.
			prepare := func(source int, frames []screenshots.ComparisonSample, at float64) (comparisons.SampledSource, []screenshots.ComparisonSample, error) {
				policy := plan.Sources[source]
				intra := comparisonIntraOnlyRegion(frames, at, durations[source])
				if policy.PreferBFrames && !intra && len(frames) > 0 &&
					!slices.ContainsFunc(frames, func(f screenshots.ComparisonSample) bool { return f.PictureType != "I" }) {
					var err error
					probeSeconds := min(9, durations[source])
					probeCenter := max(probeSeconds/2, min(at, durations[source]-probeSeconds/2))
					frames, err = sample(source, probeCenter, probeSeconds/2)
					if err != nil {
						return comparisons.SampledSource{}, nil, err
					}
					intra = comparisonIntraOnlyRegion(frames, probeCenter, durations[source])
				}
				return comparisons.SampledSource{
					ID:            policy.ID,
					PreferBFrames: policy.PreferBFrames,
					AllowedTypes:  slices.Clone(policy.AllowedFrameTypes),
					IntraOnly:     intra,
					Frames:        comparisonSamples(frames),
				}, frames, nil
			}
			sampled := make([]comparisons.SampledSource, len(plan.Sources))
			frames := map[string][]screenshots.ComparisonSample{}
			sampled[main], frames[plan.MainSourceID], err = prepare(main, localReference, center)
			if err != nil {
				if errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
					lastReason = "native_decode_budget_exhausted"
					break search
				}
				if len(result.Rows) > 0 && errors.Is(err, screenshots.ErrComparisonNormalizationUnavailable) {
					return result, observations, true, err
				}
				return failed(err, main+1)
			}
			// Keep exact main evidence when an I-only evidence probe widened the
			// native reference. The extra pictures prove cadence, not scene facts.
			for j := range sampled[main].Frames {
				frame := &sampled[main].Frames[j]
				for _, original := range reference {
					if frame.FrameID == original.FrameID {
						frame.Evidence = original.Evidence
						break
					}
				}
				if frame.Evidence != nil && !comparisons.EligibleTime(frame.TimestampSeconds, window, excluded) {
					evidence := *frame.Evidence
					evidence.Eligible, evidence.Reason = false, "protected_selection"
					frame.Evidence = &evidence
				}
			}
			matcher := comparisons.NewSceneSectionMatcher(sampled[main], candidate, category)
			order := make([]string, len(plan.Sources))
			for i, source := range plan.Sources {
				order[i] = source.ID
			}
			unavailableWindow, exhausted := false, false
			for i, source := range plan.Sources {
				if i == main {
					continue
				}
				prediction := offsets[source.ID]
				at := prediction.Predict(center)
				target, sampleErr := sample(i, at, min(32, max(3, width/2*prediction.Scale()+1.5)))
				if sampleErr == nil {
					sampled[i], target, sampleErr = prepare(i, target, at)
				}
				if sampleErr != nil {
					if errors.Is(sampleErr, screenshots.ErrComparisonFastInconclusive) {
						exhausted = true
						break
					}
					if limitation, ok := api.AsOperationFailure(sampleErr); ok &&
						(limitation.ComparisonReason == "dvd_random_access_initialization" || limitation.ComparisonReason == "transport_initialization") {
						unavailableWindow = true
						break
					}
					return comparisons.MatchResult{}, nil, false, sampleErr
				}
				frames[source.ID] = target
				viable, matchErr := matcher.AddSource(
					ctx,
					sampled[i],
					comparisons.PlaybackScale{Numerator: prediction.ScaleNumerator, Denominator: prediction.ScaleDenominator},
				)
				if matchErr != nil {
					return comparisons.MatchResult{}, nil, false, fmt.Errorf("check native comparison source: %w", matchErr)
				}
				if !viable {
					progress.logger.Tracef(
						"comparisons: phase=alignment_rows decision=retry column=%d reason=native_intersection_unavailable window_seconds=%.3f",
						i+1,
						width,
					)
					unavailableWindow = true
					break
				}
			}
			if exhausted {
				lastReason = "native_decode_budget_exhausted"
				break search
			}
			if unavailableWindow {
				continue
			}
			matched, err = matcher.Match(ctx, order, result.Rows)
			if err != nil {
				return comparisons.MatchResult{}, nil, false, fmt.Errorf("match native comparison section: %w", err)
			}
			if len(matched.Rows) == 1 && matched.Rows[0].Complete && !slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool {
				return math.Abs(row.MainSeconds-matched.Rows[0].MainSeconds) < separation
			}) {
				selectedFrames = frames
				break
			}
		}
		if selectedFrames != nil {
			commit(matchedSection{match: matched, frames: selectedFrames})
		}
	}
	if slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool { return !comparisons.MatchingPictureTypes(row) }) {
		result.Warnings = append(
			result.Warnings,
			"Some rows use mixed permitted picture types because comparable matching-type pictures were unavailable within the work allowance.",
		)
	}
	if len(result.Rows) < options.Count {
		result.Warnings = append(result.Warnings, "Some rows could not be established within the bounded matching allowance.")
		if lastReason == "" {
			lastReason = "native_candidates_exhausted"
		}
		analysis := *result.Analysis
		analysis.Reasons = append(slices.Clone(analysis.Reasons), lastReason)
		result.Analysis = &analysis
		progress.logger.Warnf("comparisons: phase=alignment_rows state=partial reason=%s rows=%d requested=%d", lastReason, len(result.Rows), options.Count)
	}
	if result.AchievedDark < result.RequestedDark || result.AchievedMotion < result.RequestedMotion || result.AchievedFlat < result.RequestedFlat {
		result.Warnings = append(
			result.Warnings,
			"Suitable scenes were unavailable or unmatched; qualified deficits remain and other eligible rows use best effort.",
		)
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

func decodeLimit(source, main, rows int) float64 {
	if source == main {
		return float64(min(900, 160+48*rows))
	}
	return float64(min(900, 64+40*rows))
}

func comparisonSampleChunkSeconds(current, seconds float64, elapsed time.Duration) float64 {
	// Short tails mostly measure setup and seeking. Keep the learned size,
	// while still reacting when even a short decode takes eight seconds.
	if seconds < 8 && elapsed < 8*time.Second {
		return current
	}
	// Keep slow decoders on short windows. Measured real-time decoding allows
	// sixteen-second batches; twice real time allows thirty-two, below the
	// decoder's existing 60-second worker cap.
	switch {
	case elapsed.Seconds() <= seconds/2:
		return 32
	case elapsed.Seconds() <= seconds:
		return 16
	default:
		return 8
	}
}

type comparisonOperationStartKey struct{}

func comparisonTelevision(retained workflowMediaPrivateArtifacts) bool {
	return strings.EqualFold(retained.screenshotSubject.MediaCategory, "tv")
}
