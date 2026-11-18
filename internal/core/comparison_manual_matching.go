// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonMainTimeAllowed(plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts, t float64) error {
	source := retained.ComparisonSources[plan.MainSourceID].Source
	duration := source.VideoDurationSeconds
	if duration <= 0 {
		duration = source.DurationSeconds
	}
	window, excluded, err := comparisons.SelectionRange(plan.Preferences, duration, comparisonTelevision(retained))
	if err != nil {
		return fmt.Errorf("comparison scene analysis: %w", err)
	}
	if !comparisons.EligibleTime(t, window, excluded) {
		return comparisonSelectionFailure(
			api.OperationFailureInvalidInput,
			"protected_time",
			"Revise the main selection range or exclusions before selecting this protected picture.",
		)
	}
	return nil
}

func (b workflowMediaBuilder) inspectManualMainScene(
	ctx context.Context,
	plan api.ComparisonPlan,
	retained workflowMediaPrivateArtifacts,
	t float64,
	budget *screenshots.ComparisonDecodeBudget,
	retentionLimit int64,
) (screenshots.ComparisonSample, error) {
	if err := comparisonMainTimeAllowed(plan, retained, t); err != nil {
		return screenshots.ComparisonSample{}, err
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return screenshots.ComparisonSample{}, err
	}
	source := retained.ComparisonSources[plan.MainSourceID].Source
	duration := source.VideoDurationSeconds
	if duration <= 0 {
		duration = source.DurationSeconds
	}
	start := max(0, t-2)
	frames, err := renderer.AnalyzeComparisonSource(ctx, screenshots.ComparisonSampleRequest{
		Budget:              budget,
		RetentionLimitBytes: retentionLimit,
		Source:              source,
		StartSeconds:        start,
		DurationSeconds:     min(4, duration-start),
		NormalizeSDR:        true,
	})
	if err != nil {
		return screenshots.ComparisonSample{}, fmt.Errorf("comparison scene analysis: %w", err)
	}
	nearest := -1
	for i, f := range frames {
		if f.RelativeTimestampSeconds >= t-1e-6 && (nearest < 0 || f.RelativeTimestampSeconds < frames[nearest].RelativeTimestampSeconds) {
			nearest = i
		}
	}
	if nearest < 0 || math.Abs(frames[nearest].RelativeTimestampSeconds-t) > .3 || frames[nearest].Evidence == nil || !frames[nearest].Evidence.Eligible {
		return screenshots.ComparisonSample{}, comparisonSelectionFailure(
			api.OperationFailureInvalidInput,
			"scene_eligibility_uncertain",
			"This main picture is a transition, title, blank or uncertain scene. Inspect another moment or revise the selection range.",
		)
	}
	selected := frames[nearest]
	if err := comparisonMainTimeAllowed(plan, retained, selected.RelativeTimestampSeconds); err != nil {
		return screenshots.ComparisonSample{}, err
	}
	cloned := *selected.Evidence
	cloned.Dark = api.ComparisonSceneCategory{State: "inconclusive", Reason: "manual_selection"}
	cloned.Motion = cloned.Dark
	cloned.Flat = cloned.Dark
	selected.Evidence = &cloned
	return selected, nil
}

// matchManualComparisonMoments uses only explicit neighborhoods. Manual anchors
// authorize their exact observed frames, never an offset outside those anchors.
func (b workflowMediaBuilder) matchManualComparisonMoments(
	ctx context.Context,
	retained workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan,
	options comparisons.MatchOptions,
	progress *comparisonProgress,
) (matchedPlan api.ComparisonPlan, matchErr error) {
	renderer, err := b.comparisonService()
	if err != nil {
		return plan, err
	}
	result := comparisons.MatchResult{
		RequestedDark:   options.DarkCount,
		RequestedMotion: options.MotionCount,
		RequestedFlat:   options.FlatCount,
	}
	observations := map[string][]screenshots.ComparisonSample{}
	renderers := map[string]*screenshots.ComparisonAnalysisRenderer{}
	defer func() {
		if len(result.Rows) > 0 && (errors.Is(matchErr, context.Canceled) || errors.Is(matchErr, context.DeadlineExceeded)) {
			matchedPlan = applyComparisonMatches(plan, result, observations, options)
			retainMatchedComparisonWitnesses(matchedPlan, observations, retained)
		}
	}()
	retainedBytes := int64(0)
	totalBytes := int64(24 << 30)
	budgets := map[string]*screenshots.ComparisonDecodeBudget{}
	for _, source := range plan.Sources {
		renderers[source.ID] = &screenshots.ComparisonAnalysisRenderer{}
		limit := decodeLimit(0, 1, options.Count)
		if source.ID == plan.MainSourceID {
			limit = decodeLimit(0, 0, options.Count)
		}
		budgets[source.ID] = &screenshots.ComparisonDecodeBudget{
			RemainingEncodedBytes: 8 << 30,
			AttemptRemaining:      &totalBytes,
			MovieSecondsLimit:     limit,
		}
	}
	matchAspect := comparisonMatchAspect(retained, plan)
	activePictures := map[string]*screenshots.ComparisonCrop{}
	detector, canInspect := renderer.(interface {
		DetectComparisonActivePicture(context.Context, screenshots.ComparisonSource, float64, float64, *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error)
	})
	inspectCrop := func(id string, center, radius float64) error {
		if !canInspect {
			return nil
		}
		source := retained.ComparisonSources[id].Source
		duration := source.VideoDurationSeconds
		if duration <= 0 {
			duration = source.DurationSeconds
		}
		start := max(0, center-radius)
		span := min(4, duration-start)
		crop, err := detector.DetectComparisonActivePicture(ctx, source, start, span, budgets[id])
		if err != nil {
			failure, optional := api.AsOperationFailure(err)
			if !optional || failure.ComparisonReason != "dvd_random_access_initialization" && failure.ComparisonReason != "transport_initialization" {
				return fmt.Errorf("inspect manual comparison picture area: %w", err)
			}
			crop = nil
		}
		activePictures[id] = crop
		return nil
	}
	sample := func(id string, center, radius float64, crop *screenshots.ComparisonCrop) ([]screenshots.ComparisonSample, error) {
		source := retained.ComparisonSources[id].Source
		duration := source.VideoDurationSeconds
		if duration <= 0 {
			duration = source.DurationSeconds
		}
		start, end := max(0, center-radius), min(duration, center+radius)
		if end <= start || budgets[id].ConsumedMovieSeconds+end-start > budgets[id].MovieSecondsLimit {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		available := (128 << 20) - (16 << 20) - retainedBytes
		if available < 4096 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		frames, err := renderer.SampleComparisonSource(ctx, screenshots.ComparisonSampleRequest{
			Renderer:            renderers[id],
			RetentionLimitBytes: available,
			Source:              source,
			StartSeconds:        start,
			DurationSeconds:     end - start,
			StrideSeconds:       .002,
			NativeFrames:        true,
			NormalizeSDR:        true,
			MatchCrop:           crop,
			MatchAspectRatio:    matchAspect,
			Budget:              budgets[id],
		})
		retainedBytes += int64(len(frames)) * 4096
		if err != nil {
			return nil, fmt.Errorf("sample manual comparison: %w", err)
		}
		return frames, nil
	}
	progress.stages = 2
	progress.begin("manual_matching", len(options.MainMoments))
	for momentIndex, moment := range options.MainMoments {
		// The guard's four-second decode is also charged to the main allowance.
		if budgets[plan.MainSourceID].ConsumedMovieSeconds+4 > budgets[plan.MainSourceID].MovieSecondsLimit {
			return plan, screenshots.ErrComparisonFastInconclusive
		}
		available := (128 << 20) - (16 << 20) - retainedBytes
		if available < 4096 {
			return plan, screenshots.ErrComparisonFastInconclusive
		}
		guard, err := b.inspectManualMainScene(ctx, plan, retained, moment, budgets[plan.MainSourceID], available)
		if err != nil {
			return plan, err
		}
		if err := inspectCrop(plan.MainSourceID, guard.RelativeTimestampSeconds, 2); err != nil {
			return plan, err
		}
		reference, err := sample(plan.MainSourceID, guard.RelativeTimestampSeconds, 2, activePictures[plan.MainSourceID])
		if err != nil {
			return plan, err
		}
		selected := comparisonObservationID(guard.ComparisonFrameObservation)
		found := false
		for i, f := range reference {
			if comparisonObservationID(f.ComparisonFrameObservation) == selected {
				found = true
				reference[i].Evidence = guard.Evidence
				break
			}
		}
		if !found {
			return plan, errors.New("the inspected main picture is unavailable; inspect its native timestamp again")
		}
		columns := make([]comparisons.SampledSource, len(plan.Sources))
		alignments := map[string]comparisons.ConstantAlignment{}
		for i, source := range plan.Sources {
			columns[i] = comparisons.SampledSource{
				ID:            source.ID,
				PreferBFrames: source.PreferBFrames,
				AllowedTypes:  slices.Clone(source.AllowedFrameTypes),
			}
			if source.ID == plan.MainSourceID {
				columns[i].Frames = comparisonSamples(reference)
				// This exact main anchor comes from the native eligibility guard,
				// not from MainFrameID (which automatic section matching also uses).
				columns[i].Anchors = []comparisons.MatchAnchor{{MainSeconds: guard.RelativeTimestampSeconds, SourceSeconds: guard.RelativeTimestampSeconds}}
				observations[source.ID] = append(observations[source.ID], reference...)
				continue
			}
			center := guard.RelativeTimestampSeconds
			knownNeighborhood := false
			profile, err := b.comparisonCalibrationProfile(retained, plan, source.ID)
			if err != nil {
				return plan, err
			}
			if prediction, ok := retained.ComparisonSources[source.ID].Calibration.prediction(profile, retained.ComparisonSources[source.ID].Source); ok {
				center, knownNeighborhood = prediction.Predict(center), true
			}
			for _, anchor := range plan.Anchors {
				if anchor.SourceID == source.ID &&
					(math.Abs(anchor.MainSeconds-moment) < 1e-6 || math.Abs(anchor.MainSeconds-guard.RelativeTimestampSeconds) < 1e-6) {
					center = anchor.SourceSeconds
					knownNeighborhood = true
					columns[i].Anchors = append(
						columns[i].Anchors,
						comparisons.MatchAnchor{MainSeconds: guard.RelativeTimestampSeconds, SourceSeconds: anchor.SourceSeconds},
					)
				}
			}
			if knownNeighborhood {
				if err := inspectCrop(source.ID, center, 2); err != nil {
					return plan, err
				}
			} else {
				delete(activePictures, source.ID)
			}
			target, err := sample(source.ID, center, 6, activePictures[source.ID])
			if err != nil {
				return plan, err
			}
			if len(columns[i].Anchors) == 0 {
				discover := func(reference, target []screenshots.ComparisonSample) (comparisons.ConstantAlignment, string) {
					alignment, reason := comparisons.DiscoverAlignment(comparisonSamples(reference), comparisonSamples(target))
					if reason != "" {
						return comparisons.DiscoverPlaybackAlignment(
							comparisonSamples(reference),
							comparisonSamples(target),
							comparisons.PlaybackScale{Numerator: 1, Denominator: 1},
						)
					}
					return alignment, reason
				}
				alignment, reason := discover(reference, target)
				if reason != "" && !knownNeighborhood {
					// Bootstrap crop only inside the candidate window just decoded,
					// using its same initialization point. This is not a selected
					// moment or authority; inspect that moment after native proof.
					if err := inspectCrop(source.ID, center, 6); err != nil {
						return plan, err
					}
					if activePictures[source.ID] != nil {
						target, err = sample(source.ID, center, 6, activePictures[source.ID])
						if err != nil {
							return plan, err
						}
						alignment, reason = discover(reference, target)
					}
				}
				if reason == "" && !knownNeighborhood {
					previousCrop := activePictures[source.ID]
					if err := inspectCrop(source.ID, alignment.Predict(guard.RelativeTimestampSeconds), 2); err != nil {
						return plan, err
					}
					crop := activePictures[source.ID]
					if (crop == nil) != (previousCrop == nil) || crop != nil && previousCrop != nil && *crop != *previousCrop {
						target, err = sample(source.ID, center, 6, activePictures[source.ID])
						if err != nil {
							return plan, err
						}
					}
					// Neither bootstrap pixels nor a crop prediction authorize a
					// row. Re-establish correspondence against the final reference.
					alignment, reason = discover(reference, target)
				}
				if reason != "" {
					return plan, comparisonSelectionFailure(
						api.OperationFailureInvalidSource,
						"manual_correspondence_inconclusive",
						"Inspect an exact correspondence in each source for this main moment.",
					)
				}
				alignments[source.ID] = alignment
			}
			columns[i].Frames = comparisonSamples(target)
			observations[source.ID] = append(observations[source.ID], target...)
		}
		previous := slices.DeleteFunc(slices.Clone(result.Rows), func(row comparisons.MatchedRow) bool { return !row.Complete })
		matched, err := comparisons.AlignedMatch(columns, plan.MainSourceID, comparisons.MatchOptions{Count: 1, MainFrameID: selected}, alignments, previous)
		if err != nil {
			return plan, fmt.Errorf("comparison scene analysis: %w", err)
		}
		if len(matched.Rows) == 0 {
			return plan, comparisonSelectionFailure(
				api.OperationFailureInvalidSource,
				"manual_frame_unverified",
				"This exact native moment could not be verified in every source. Inspect explicit correspondences.",
			)
		}
		result.Rows = append(result.Rows, matched.Rows...)
		// Only chosen pictures need replay after this row. Keep earlier selected
		// witnesses, releasing the dense local windows before the next row.
		retainedBytes = 0
		for id, frames := range observations {
			observations[id] = slices.Clone(slices.DeleteFunc(frames, func(frame screenshots.ComparisonSample) bool {
				return !slices.ContainsFunc(result.Rows, func(row comparisons.MatchedRow) bool {
					return slices.ContainsFunc(row.Frames, func(chosen comparisons.FrameMatch) bool {
						return chosen.SourceID == id && chosen.FrameID == comparisonObservationID(frame.ComparisonFrameObservation)
					})
				})
			}))
			retainedBytes += int64(len(observations[id])) * 4096
		}
		progress.update(momentIndex+1, len(options.MainMoments))
	}
	progress.complete()
	if options.DarkCount+options.MotionCount+options.FlatCount > 0 {
		result.Warnings = append(result.Warnings, "Explicitly selected pictures retain no inferred scene category; requested qualified targets remain unmet.")
	}
	plan = applyComparisonMatches(plan, result, observations, options)
	retainMatchedComparisonWitnesses(plan, observations, retained)
	return plan, nil
}
