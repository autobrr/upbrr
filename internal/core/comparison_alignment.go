// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
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
	defer func() {
		// Close an early, accepted result at the phase that owns it. Optional
		// probe failures can still allow the current phase to continue.
		if returnedErr == nil && accepted && progress.active {
			progress.complete()
		}
	}()
	lastReason := ""
	noteInconclusive := func(reason string, column int) {
		lastReason = reason
		phase := progress.phase
		if phase == "" {
			phase = "alignment_eligibility"
		}
		progress.logger.Infof("comparisons: phase=%s decision=inconclusive reason=%s column=%d", phase, reason, column)
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
	progress.stages = 4
	type windowKey struct {
		source          int
		start, duration float64
	}
	cache := map[windowKey][]screenshots.ComparisonSample{}
	secondaryCache := comparisonWindowCache{}
	defer func() {
		progress.logger.Debugf("comparisons: phase=alignment_cache peak_bytes=%d evictions=%d", secondaryCache.peak, secondaryCache.evictions)
	}()
	var mainInventory, quickInventory *comparisonSceneInventory
	var inventoryOwner string
	analysisBytes := int64(0)
	retainedBytes := int64(0)
	candidateBytes := int64(0)
	prepareWindow := func(frames []comparisons.FrameSample) (int64, error) {
		// Signatures live alongside decoded samples and are shared by all scale
		// and neighborhood trials. Reserve their backing storage only once.
		var bytes int64
		for _, frame := range frames {
			bytes += int64(8*len(frame.Luma) + 192)
		}
		limit := (112 << 20) - candidateBytes - retainedBytes - analysisBytes - bytes
		secondaryCache.trim(min(16<<20, limit))
		if limit < 0 {
			return 0, screenshots.ErrComparisonFastInconclusive
		}
		comparisons.PrepareVisualSignatures(frames)
		analysisBytes += bytes
		return bytes, nil
	}
	renderers := make([]screenshots.ComparisonAnalysisRenderer, len(plan.Sources))
	sampleChunkSeconds := make([]float64, len(plan.Sources))
	budgets := make([]screenshots.ComparisonDecodeBudget, len(plan.Sources))
	aggregateBytes := int64(24 << 30)
	for i := range budgets {
		sampleChunkSeconds[i] = 8
		budgets[i].RemainingEncodedBytes = 8 << 30
		budgets[i].AttemptRemaining = &aggregateBytes
		budgets[i].MovieSecondsLimit = comparisonAutomaticDecodeLimit(options.Count)
	}
	// Complete Deep coverage includes one second of classifier context on each
	// side of every minimum eight-second chunk, plus bounded decoder admission
	// probes/retries. Keep this owner across every main window and crop probe.
	deepBudget := screenshots.ComparisonDecodeBudget{RemainingEncodedBytes: 1 << 62, MovieSecondsLimit: 4 * (duration + 2*math.Ceil(duration/8) + 16)}
	defer func() {
		if plan.Preferences.ScanMode == "deep" {
			progress.logger.Debugf(
				"comparisons: phase=discovery_budget requested_seconds=%.6f encoded_span_bytes=%d process_starts=%d",
				deepBudget.ConsumedMovieSeconds,
				(1<<62)-deepBudget.RemainingEncodedBytes,
				deepBudget.ProcessStarts,
			)
		}
	}()
	defer func() {
		for i := range plan.Sources {
			progress.logger.Debugf(
				"comparisons: phase=alignment_budget column=%d requested_seconds=%.6f encoded_span_bytes=%d remaining_seconds=%.6f remaining_span_bytes=%d process_starts=%d",
				i+1,
				budgets[i].ConsumedMovieSeconds,
				(8<<30)-budgets[i].RemainingEncodedBytes,
				budgets[i].MovieSecondsLimit-budgets[i].ConsumedMovieSeconds,
				budgets[i].RemainingEncodedBytes,
				budgets[i].ProcessStarts,
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
		budget := &budgets[source]
		if source == main && plan.Preferences.ScanMode == "deep" {
			budget = &deepBudget
		}
		crop, err := detector.DetectComparisonActivePicture(workCtx, input, start, seconds, budget)
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
	type samplingWork struct {
		wall              time.Duration
		requested, reused float64
	}
	work := make([][2]samplingWork, len(plan.Sources))
	defer func() {
		for i, column := range work {
			for phase, totals := range column {
				progress.logger.Debugf(
					"comparisons: phase=alignment_sampling column=%d rows=%t elapsed_seconds=%.3f requested_seconds=%.6f reused_seconds=%.6f",
					i+1,
					phase == 1,
					totals.wall.Seconds(),
					totals.requested,
					totals.reused,
				)
			}
		}
	}()
	var discoveryDecode func(context.Context, float64, float64, bool) ([]screenshots.ComparisonSample, error)
	sample := func(source int, center, radius float64) ([]screenshots.ComparisonSample, error) {
		start, end := max(0, center-radius), min(durations[source], center+radius)
		windowDuration := min(64, end-start)
		if end-start < 1.1 {
			return nil, nil
		}
		phase := 0
		if matchingRows {
			phase = 1
		}
		stats := &work[source][phase]
		stats.requested += windowDuration
		key := windowKey{source, start, windowDuration}
		if frames, ok := cache[key]; ok {
			stats.reused += windowDuration
			return frames, nil
		}
		if source == main && mainInventory != nil {
			admit := secondaryCache.reserve((112 << 20) - candidateBytes - retainedBytes - analysisBytes)
			var frames []screenshots.ComparisonSample
			full := false
			if err := readComparisonInventoryRange(
				ctx,
				mainInventory,
				inventoryOwner,
				&api.ComparisonTimeInterval{Start: start, End: end},
				func(frame screenshots.ComparisonSample) bool {
					if frame.RelativeTimestampSeconds >= start && frame.RelativeTimestampSeconds < end {
						if !admit(int64(len(frames)+1) * 4096) {
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
			if matchingRows && slices.ContainsFunc(frames, func(f screenshots.ComparisonSample) bool {
				return f.Replay == nil || f.Replay.Version != 2 && !f.Replay.CanReinspectDisc()
			}) {
				// Legacy archives still rank scenes and calibrate the search. A
				// freshly observed neighborhood must authorize the actual row.
				frames = nil
				from, to := max(0, start-1), min(durations[main], end+1)
				workCtx, finishWindow := progress.decodeWindow(ctx, main+1, matchedRows+1)
				updated, err := discoveryDecode(workCtx, from, to-from, false)
				finishWindow(err)
				if err != nil {
					return nil, err
				}
				for _, f := range updated {
					if f.RelativeTimestampSeconds >= start && f.RelativeTimestampSeconds < end && f.Replay != nil && f.Replay.Version == 2 {
						frames = append(frames, f)
					}
				}
			} else {
				stats.reused += windowDuration
			}
			retainedBytes += int64(len(frames)) * 4096
			if err := verifyComparisonFileJoins(frames); err != nil {
				return nil, err
			}
			cache[key] = frames
			return frames, nil
		}
		if end-start > 64+1e-9 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		row := 0
		if matchingRows {
			row = matchedRows + 1
		}
		report := progress.window(source+1, row)
		updateWindow := func(status api.StageStatus, fraction int) {
			report(status, fraction)
			if !matchingRows {
				progress.update(item*1000+fraction, total*1000)
			}
		}
		decoding := false
		defer func() {
			if decoding {
				report(api.StageStatusSkipped, 0)
			}
		}()
		var frames []screenshots.ComparisonSample
		missing := []api.ComparisonTimeInterval{{Start: start, End: end}}
		if source == main && quickInventory != nil {
			// Only archived native pixels can fill a calibration/probe gap.
			// Section scoring also records coverage, without archiving its rasters.
			var archived []screenshots.ComparisonSample
			limit := min(int64(16<<20), (112<<20)-candidateBytes-retainedBytes-analysisBytes)
			admit := secondaryCache.reserve((112 << 20) - candidateBytes - retainedBytes - analysisBytes)
			full := false
			err := readComparisonInventoryRange(
				ctx,
				quickInventory,
				inventoryOwner,
				&api.ComparisonTimeInterval{Start: start, End: end},
				func(frame screenshots.ComparisonSample) bool {
					if needed := int64(len(archived)+1) * 4096; needed > limit || !admit(needed) {
						full = true
						return false
					}
					archived = append(archived, frame)
					return true
				},
			)
			if err != nil {
				return nil, err
			}
			if !full {
				archived, err = orderedComparisonSamples(archived)
				if err != nil {
					return nil, err
				}
				var covered comparisonWindowCache
				covered.put(main, start, end, renderers[main].Mode, archived, limit)
				frames, missing, err = covered.get(main, start, end, renderers[main].Mode)
				if err != nil {
					return nil, err
				}
				retainedBytes += int64(len(frames)) * 4096
			}
		}
		if source != main {
			secondaryCache.trim(min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes))
			var cacheErr error
			frames, missing, cacheErr = secondaryCache.get(source, start, end, renderers[source].Mode)
			if cacheErr != nil {
				return nil, cacheErr
			}
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
		unseen := 0.0
		for _, gap := range missing {
			unseen += gap.End - gap.Start
		}
		stats.reused += max(0, windowDuration-unseen)
		var decodedSeconds float64
		for _, gap := range missing {
			if budgets[source].ConsumedMovieSeconds+gap.End-gap.Start > budgets[source].MovieSecondsLimit {
				return nil, screenshots.ErrComparisonFastInconclusive
			}
			for chunkStart := gap.Start; chunkStart < gap.End-1e-9; {
				chunkDuration := min(sampleChunkSeconds[source], gap.End-chunkStart)
				available := int64(112<<20) - candidateBytes - retainedBytes - analysisBytes
				if available < 4096 {
					return nil, screenshots.ErrComparisonFastInconclusive
				}
				request := screenshots.ComparisonSampleRequest{
					Retain:           secondaryCache.reserve(available),
					MatchCrop:        activePictures[source],
					Budget:           &budgets[source],
					Renderer:         &renderers[source],
					Source:           retained.ComparisonSources[plan.Sources[source].ID].Source,
					StartSeconds:     chunkStart,
					DurationSeconds:  chunkDuration,
					StrideSeconds:    .002,
					NativeFrames:     true,
					NormalizeSDR:     true,
					MatchAspectRatio: matchAspect,
				}
				workCtx := progress.child(ctx, func(completed int) {
					fraction := (decodedSeconds + chunkDuration*float64(completed)/1000) / unseen
					updateWindow(api.StageStatusRunning, min(999, int(fraction*1000)))
				})
				decoding = true
				updateWindow(api.StageStatusRunning, min(999, int(1000*decodedSeconds/unseen)))
				started := time.Now()
				decoded, err := renderer.SampleComparisonSource(workCtx, request)
				stats.wall += time.Since(started)

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
				if source != main {
					secondaryCache.put(
						source,
						chunkStart,
						chunkStart+chunkDuration,
						renderers[source].Mode,
						decoded,
						min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes),
					)
				}
				frames = append(frames, decoded...)
				chunkStart += chunkDuration
				decodedSeconds += chunkDuration
			}
		}
		frames, orderErr := orderedComparisonSamples(frames)
		if orderErr != nil {
			return nil, orderErr
		}
		if err := verifyComparisonFileJoins(frames); err != nil {
			return nil, err
		}
		cache[key] = frames
		decoding = false
		updateWindow(api.StageStatusCompleted, 1000)
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
	discoveryDecode = func(workCtx context.Context, start, seconds float64, deep bool) ([]screenshots.ComparisonSample, error) {
		// A compatible complete Deep inventory already contains normalized main
		// pixels. Probe the main crop only when fresh analysis actually starts.
		if !mainPictureReady {
			if err := detectPicture(workCtx, main); err != nil {
				return nil, err
			}
			mainPictureReady = true
		}
		if !deep {
			if frames := secondaryCache.classified(start, start+seconds, renderers[main].Mode); len(frames) > 0 {
				return frames, nil
			}
		}
		available := int64(112<<20) - candidateBytes - retainedBytes - analysisBytes
		if available < 4096 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		budget := &budgets[main]
		if deep {
			budget = &deepBudget
		}
		frames, err := analyzer.AnalyzeComparisonSource(workCtx, screenshots.ComparisonSampleRequest{
			Retain:           secondaryCache.reserve(available),
			MatchCrop:        activePictures[main],
			Source:           retained.ComparisonSources[plan.MainSourceID].Source,
			StartSeconds:     start,
			DurationSeconds:  seconds,
			StrideSeconds:    .002,
			NativeFrames:     true,
			NormalizeSDR:     true,
			MatchAspectRatio: matchAspect,
			Budget:           budget,
			Renderer:         &renderers[main],
		})
		if err != nil {
			return frames, fmt.Errorf("analyze main comparison section: %w", err)
		}
		if !deep {
			secondaryCache.retain(comparisonCachedWindow{
				classified: true,
				source:     main,
				start:      start,
				end:        start + seconds,
				renderer:   renderers[main].Mode,
				frames:     frames,
			},
				min(16<<20, (112<<20)-candidateBytes-retainedBytes-analysisBytes-int64(len(frames))*4096))
		}
		return frames, nil
	}
	priorInventory := retained.ComparisonInventory
	quick := &comparisonQuickSearch{}
	inventory, discoveryErr := b.discoverComparisonScenes(discoveryCtx, retained, plan, window, excluded, discoveryDecode, progress, quick)
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
	if plan.Preferences.ScanMode != "deep" {
		quickInventory, inventoryOwner = inventory, owner
	}
	defer func() {
		_, incomplete := errors.AsType[*releaseworkflow.ComparisonIncompleteError](returnedErr)
		if len(quick.parts) > 0 && returnedErr != nil && !incomplete && !comparisonCancellationOnly(returnedErr) {
			quick.cleanup()
			retained.ComparisonInventory = priorInventory
			return
		}
		if err := quick.finish(ctx, owner, retained); err != nil {
			returned, returnedObservations, accepted, returnedErr = comparisons.MatchResult{}, nil, false, &comparisonPublicationError{err}
		}
	}()
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
	candidateBytes += comparisonInventoryIndexBytes(inventory) + quick.memoryBytes()
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
		references := make([][]comparisons.FrameSample, len(centers))
		for index, center := range centers {
			var region []screenshots.ComparisonSample
			for _, frame := range referenceWindow {
				if frame.RelativeTimestampSeconds >= center-2 && frame.RelativeTimestampSeconds < center+2 {
					region = append(region, frame)
				}
			}
			references[index] = comparisonSamples(region)
			if _, err := prepareWindow(references[index]); err != nil {
				return failed(err, main+1)
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
			preparedTarget := comparisonSamples(target)
			targetBytes, err := prepareWindow(preparedTarget)
			if err != nil {
				return failed(err, i+1)
			}
			hypotheses[source.ID] = comparisons.NativePlaybackScales(comparisonSamples(referenceWindow), preparedTarget, hypotheses[source.ID])
			if len(hypotheses[source.ID]) == 0 {
				noteInconclusive("rate_hypothesis_limit", i+1)
				return unavailable(), nil, true, nil
			}
			for index, center := range centers {
				preparedReference := references[index]
				for _, scale := range hypotheses[source.ID] {
					alignment, reason := comparisons.DiscoverPlaybackAlignment(preparedReference, preparedTarget, scale)
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
			analysisBytes -= targetBytes
			// This column has finished every center and scale. Only the bounded
			// secondary cache keeps decoded target intervals for later row work.
			for key, frames := range cache {
				if key.source == i {
					retainedBytes -= int64(len(frames)) * 4096
					delete(cache, key)
				}
			}
		}
		// Native region proofs own their checked points, not the decoded rasters.
		// Release those windows before admitting any additional calibration region.
		clear(cache)
		retainedBytes, analysisBytes = 0, 0
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
	rowsStarted := time.Now()
	defer func() {
		progress.logger.Debugf("comparisons: phase=alignment_rows_total elapsed_seconds=%.3f rows=%d", time.Since(rowsStarted).Seconds(), matchedRows)
	}()
	matchingRows = true
	observations := map[string][]screenshots.ComparisonSample{}
	result := comparisons.MatchResult{
		RequestedDark:   options.DarkCount,
		RequestedMotion: options.MotionCount,
		RequestedFlat:   options.FlatCount,
		Analysis:        &inventory.Summary,
	}
	targets := [3]int{options.MotionCount, options.DarkCount, options.FlatCount}
	categoryAttempts := [3]int{}
	used := map[string]bool{}
	var attemptedSections []api.ComparisonTimeInterval
	type matchedSection struct {
		match  comparisons.MatchResult
		frames map[string][]screenshots.ComparisonSample
	}
	var deferredSeeds [3]*comparisons.FrameSample
	var pendingRows [3]*matchedSection
	commit := func(section matchedSection) {
		result.Rows = append(result.Rows, section.match.Rows...)
		matchedRows = len(result.Rows)
		result.AchievedDark += section.match.AchievedDark
		result.AchievedMotion += section.match.AchievedMotion
		result.AchievedFlat += section.match.AchievedFlat
		for id, decoded := range section.frames {
			observations[id] = append(observations[id], decoded...)
		}
		progress.item(
			strconv.Itoa(len(result.Rows)),
			"comparison_row",
			fmt.Sprintf("Row %d", len(result.Rows)),
			api.StageStatusCompleted,
			1000,
		)
		progress.update(len(result.Rows), options.Count)
	}
	// Calibration rasters are no longer needed once their checked predictors exist.
	clear(cache)
	retainedBytes = 0
	separation := (window.End - window.Start) / float64(max(1, options.Count*3))
	availablePending := func(section matchedSection) bool {
		row := section.match.Rows[0]
		return !slices.ContainsFunc(result.Rows, func(previous comparisons.MatchedRow) bool {
			if math.Abs(previous.MainSeconds-row.MainSeconds) < separation {
				return true
			}
			for i, frame := range row.Frames {
				if math.Abs(previous.Frames[i].TimestampSeconds-frame.TimestampSeconds) < 1e-6 {
					return true
				}
			}
			return false
		})
	}
	flushPending := func() {
		for c, section := range pendingRows {
			if section != nil && len(result.Rows) < options.Count && availablePending(*section) {
				commit(*section)
			}
			pendingRows[c] = nil
		}
	}
	// Classified decoder stops preserve independently verified rows. Identity and
	// source failures remain closed; publication revalidates retained bindings.
	defer func() {
		if comparisonCancellationOnly(returnedErr) ||
			errors.Is(returnedErr, screenshots.ErrComparisonWorkerFailed) ||
			errors.Is(returnedErr, screenshots.ErrComparisonNormalizationUnavailable) {
			flushPending()
		}
		workerStopped := len(result.Rows) > 0 && (errors.Is(returnedErr, screenshots.ErrComparisonWorkerFailed) ||
			errors.Is(returnedErr, screenshots.ErrComparisonNormalizationUnavailable))
		if workerStopped {
			returnedErr = &releaseworkflow.ComparisonIncompleteError{Cause: returnedErr, Reason: "match_worker_failed"}
		}
		if workerStopped || comparisonCancellationOnly(returnedErr) {
			returned, returnedObservations, accepted = result, observations, true
		}
	}()
	attemptLimit := options.Count * 30
search:
	for attempt := 0; attempt < attemptLimit && len(result.Rows) < options.Count; attempt++ {
		progress.update(attempt, attemptLimit)
		clear(cache)
		retainedBytes, analysisBytes = 0, 0
		for _, decoded := range observations {
			retainedBytes += int64(len(decoded)) * 4096
		}
		requestedCategory := comparisonSceneCategory(targets, [3]int{result.AchievedMotion, result.AchievedDark, result.AchievedFlat}, categoryAttempts)
		if requestedCategory < 0 {
			before := len(result.Rows)
			flushPending()
			if len(result.Rows) > before {
				continue
			}
		}
		searchTargets := [3]int{}
		if requestedCategory >= 0 {
			searchTargets[requestedCategory] = 1
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
					if frame.Unenclosed {
						_, ok := inventory.Enclosed[frame.FrameID]
						frame.Unenclosed = !ok
					}
					if !visit(frame.sample()) {
						break
					}
				}
				return
			}
			if inventory.Summary.ScanMode != "deep" {
				inventoryErr = streamQuickRanking(ctx, inventory, owner, func(candidate comparisonSceneCandidate) bool { return visit(candidate.sample()) })
				return
			}
			inventoryErr = readComparisonInventory(ctx, inventory, owner, func(frame screenshots.ComparisonSample) bool {
				return visit(comparisonSamples([]screenshots.ComparisonSample{frame})[0])
			})
		}
		var candidate comparisons.FrameSample
		category, found, usingFallback := -1, false, false
		if requestedCategory < 0 {
			for c, seed := range deferredSeeds {
				deferredSeeds[c] = nil
				if seed != nil && !used[seed.FrameID] && !slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool {
					return math.Abs(row.MainSeconds-seed.TimestampSeconds) < separation
				}) {
					candidate, category, found, usingFallback = *seed, c, true, true
					break
				}
			}
		}
		if !found {
			candidate, category, found = comparisons.SelectScene(sequence, used, result.Rows, searchTargets, [3]int{}, searchTargets, separation)
		}
		if inventoryErr != nil {
			return comparisons.MatchResult{}, nil, false, inventoryErr
		}
		if found && requestedCategory >= 0 && !comparisons.SceneQualified(candidate.Evidence, requestedCategory) {
			// No qualified candidate remains in the admitted inventory. Give
			// every other requested category its opportunity before general rows.
			categoryAttempts[requestedCategory] = 10 * targets[requestedCategory]
			deferredSeeds[requestedCategory] = new(candidate)
			continue
		}
		if !found {
			if !inventory.Summary.RankingComplete {
				lastReason = "main_discovery_incomplete"
				break
			}
			if plan.Preferences.ScanMode != "deep" && inventory.QuickWindows < comparisonQuickWindows &&
				budgets[main].ConsumedMovieSeconds+4 <= budgets[main].MovieSecondsLimit {
				expanded, err := b.discoverComparisonScenes(ctx, retained, plan, window, excluded, discoveryDecode, progress, quick)
				if err != nil {
					return comparisons.MatchResult{}, nil, false, err
				}
				advanced := expanded.QuickWindows > inventory.QuickWindows
				inventory = expanded
				quickInventory = expanded
				result.Analysis = &inventory.Summary
				progress.begin("alignment_rows", options.Count*len(plan.Sources))
				if advanced {
					part := quick.parts[len(quick.parts)-1]
					candidateBytes += comparisonInventoryIndexBytes(part) + 1024
					for _, block := range part.Blocks {
						candidateBytes += int64(block.Frames) * comparisonQuickKeyBytes
					}
					if candidates != nil {
						more, size, err := comparisonInventoryCandidates(ctx, part, owner, (64<<20)-candidateBytes)
						if err != nil {
							return comparisons.MatchResult{}, nil, false, err
						}
						if more == nil && part.Bytes != 0 {
							candidates, candidateBytes = nil, quick.memoryBytes()
						} else {
							candidates = mergeSceneCandidates(candidates, more)
							candidateBytes += size
						}
					}
					continue
				}
			}
			if requestedCategory >= 0 {
				categoryAttempts[requestedCategory] = 10 * targets[requestedCategory]
				continue
			}
			break
		}
		if requestedCategory >= 0 {
			categoryAttempts[requestedCategory]++
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
			// Quick discovery already computed real evidence and native pixels
			// for these pictures. Keep all of them instead of throwing away the
			// native alternatives between sparse spatial measurements.
			quickAnchor, reference, err = comparisonInventoryAnchorWindow(ctx, inventory, owner, candidate.FrameID, center)
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
		var signatures int64
		for _, width := range widths {
			analysisBytes -= signatures
			signatures = 0
			if mainInventory == nil && category >= 0 && width == 12 {
				// Only a failed compact native match pays for wider scene scoring.
				inventory = cloneComparisonInventory(inventory)
				retained.ComparisonInventory, result.Analysis = inventory, &inventory.Summary
				reference, err = scoreComparisonSection(ctx, inventory, quickAnchor, quick.seen,
					func(workCtx context.Context, start, seconds float64) ([]screenshots.ComparisonSample, error) {
						workCtx, finishWindow := progress.decodeWindow(workCtx, main+1, matchedRows+1)
						frames, err := discoveryDecode(workCtx, start, seconds, false)
						finishWindow(err)
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
				if errors.Is(err, screenshots.ErrComparisonNormalizationUnavailable) {
					flushPending()
					if len(result.Rows) > 0 {
						return result, observations, true, err
					}
				}
				return failed(err, main+1)
			}
			// Keep exact main evidence when an I-only evidence probe widened the
			// native reference. The extra pictures prove cadence, not scene facts.
			for j, decoded := range frames[plan.MainSourceID] {
				frame := &sampled[main].Frames[j]
				frame.Evidence, err = comparisonSectionFrameEvidence(reference, decoded.ComparisonFrameObservation)
				if err != nil {
					return comparisons.MatchResult{}, nil, false, err
				}
				if frame.Evidence != nil && !comparisons.EligibleTime(frame.TimestampSeconds, window, excluded) {
					evidence := *frame.Evidence
					evidence.Eligible, evidence.Reason = false, "protected_selection"
					frame.Evidence = &evidence
				}
			}
			signatures, err = prepareWindow(sampled[main].Frames)
			if err != nil {
				lastReason = "native_decode_budget_exhausted"
				break search
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
				size, err := prepareWindow(sampled[i].Frames)
				if err != nil {
					exhausted = true
					break
				}
				signatures += size
				viable, matchErr := matcher.AddSource(
					ctx,
					sampled[i],
					comparisons.PlaybackScale{Numerator: prediction.ScaleNumerator, Denominator: prediction.ScaleDenominator},
				)
				if matchErr != nil {
					return comparisons.MatchResult{}, nil, false, fmt.Errorf("check native comparison source: %w", matchErr)
				}
				if !viable {
					lastReason = matcher.InconclusiveReason()
					progress.logger.Tracef(
						"comparisons: phase=alignment_rows decision=retry column=%d reason=%s window_seconds=%.3f",
						i+1,
						lastReason,
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
			section := matchedSection{match: matched, frames: map[string][]screenshots.ComparisonSample{}}
			retainComparisonRowSamples(section.frames, selectedFrames, matched.Rows[0])
			if category >= 0 && !usingFallback && !comparisons.SceneQualified(matched.Rows[0].Evidence, category) {
				if pendingRows[category] == nil || comparisons.PreferSceneRow(matched.Rows[0], pendingRows[category].match.Rows[0], category) {
					pendingRows[category] = &section
				}
			} else {
				commit(section)
			}
		}
	}
	flushPending()
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
	progress.logger.Infof("comparisons: phase=alignment_rows decision=bounded_local rows=%d sources=%d", len(result.Rows), len(plan.Sources))
	return result, observations, true, nil
}

func comparisonSectionFrameEvidence(
	reference []screenshots.ComparisonSample,
	decoded screenshots.ComparisonFrameObservation,
) (*api.ComparisonSceneEvidence, error) {
	id := comparisonObservationID(decoded)
	for _, original := range reference {
		if comparisonObservationID(original.ComparisonFrameObservation) == id {
			// Separate reads of a historical Deep archive deserialize its saved
			// span twice. Exact stored equality transfers classification only.
			stored := original.Replay.CanReinspectDisc() && decoded.Replay.CanReinspectDisc() &&
				comparisonTimingOf(original.ComparisonFrameObservation) == comparisonTimingOf(decoded) &&
				reflect.DeepEqual(original.Replay, decoded.Replay)
			if comparisonRecord(original.ComparisonFrameObservation).matches(decoded) || stored {
				return original.Evidence, nil
			}
			return nil, comparisonWitnessConflict()
		}
	}
	return nil, nil
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
