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
	request screenshots.ComparisonSampleRequest,
) (screenshots.ComparisonSample, []screenshots.ComparisonSample, error) {
	if err := comparisonMainTimeAllowed(plan, retained, t); err != nil {
		return screenshots.ComparisonSample{}, nil, err
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return screenshots.ComparisonSample{}, nil, err
	}
	source := retained.ComparisonSources[plan.MainSourceID].Source
	duration := source.VideoDurationSeconds
	if duration <= 0 {
		duration = source.DurationSeconds
	}
	start := max(0, t-2)
	request.Source, request.StartSeconds, request.DurationSeconds = source, start, min(4, duration-start)
	request.NormalizeSDR = true
	frames, err := renderer.AnalyzeComparisonSource(ctx, request)
	if err != nil {
		return screenshots.ComparisonSample{}, nil, fmt.Errorf("comparison scene analysis: %w", err)
	}
	nearest := -1
	for i, f := range frames {
		if f.RelativeTimestampSeconds >= t-1e-6 && (nearest < 0 || f.RelativeTimestampSeconds < frames[nearest].RelativeTimestampSeconds) {
			nearest = i
		}
	}
	if nearest < 0 || frames[nearest].Replay.MissingBoundary() || math.Abs(frames[nearest].RelativeTimestampSeconds-t) > .3 ||
		frames[nearest].Evidence == nil ||
		!frames[nearest].Evidence.Eligible {
		return screenshots.ComparisonSample{}, nil, comparisonSelectionFailure(
			api.OperationFailureInvalidInput,
			"scene_eligibility_uncertain",
			"This main picture is a transition, title, blank or uncertain scene. Inspect another moment or revise the selection range.",
		)
	}
	selected := frames[nearest]
	if err := comparisonMainTimeAllowed(plan, retained, selected.RelativeTimestampSeconds); err != nil {
		return screenshots.ComparisonSample{}, nil, err
	}
	cloned := *selected.Evidence
	cloned.Dark = api.ComparisonSceneCategory{State: "inconclusive", Reason: "manual_selection"}
	cloned.Motion = cloned.Dark
	cloned.Flat = cloned.Dark
	selected.Evidence = &cloned
	return selected, frames, nil
}

func comparisonManualReplayAt(
	plan api.ComparisonPlan, source workflowComparisonSource, sourceID string,
	mainMoment, sourceMoment float64,
) (*screenshots.ComparisonFrameReplay, error) {
	var selected *screenshots.ComparisonFrameReplay
	for _, row := range plan.Rows {
		if row.Excluded || !slices.ContainsFunc(row.Cells, func(cell api.ComparisonCell) bool {
			return cell.SourceID == plan.MainSourceID && cell.TimestampSeconds != nil && *cell.TimestampSeconds == mainMoment && cell.Confidence == nil
		}) {
			continue
		}
		for _, cell := range row.Cells {
			if cell.SourceID != sourceID || cell.Confidence != nil || cell.TimestampSeconds == nil ||
				math.Abs(*cell.TimestampSeconds-sourceMoment) > 1e-6 || cell.ObservedIdentity == nil {
				continue
			}
			key, err := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
			if err != nil {
				return nil, fmt.Errorf("identify selected comparison picture: %w", err)
			}
			witness, found := source.Witnesses[string(key)]
			if !found {
				return nil, comparisonWitnessConflict()
			}
			if witness.Identity != *cell.ObservedIdentity {
				return nil, comparisonWitnessConflict()
			}
			// A timestamp edit can leave the previous observed identity on the
			// cell. Only a witness for the requested moment still pins a picture.
			inspectedSeconds := *cell.TimestampSeconds
			if source.Source.Input != nil {
				inspectedSeconds = float64(witness.TitleTime90) / 90000
			} else if witness.Identity.TimeBaseDenominator > 0 {
				inspectedSeconds = float64(witness.Identity.PTS)*float64(witness.Identity.TimeBaseNumerator)/
					float64(witness.Identity.TimeBaseDenominator) - source.Source.StartTimeSeconds
			}
			if math.Abs(inspectedSeconds-*cell.TimestampSeconds) > 1e-6 {
				continue
			}
			if selected != nil && selected.Identity != witness.Identity {
				return nil, comparisonWitnessConflict()
			}
			selected = new(witness)
		}
	}
	return selected, nil
}

func verifyManualComparisonWitness(expected *screenshots.ComparisonFrameReplay, observed screenshots.ComparisonFrameObservation) error {
	if expected != nil && (observed.Identity == nil || *observed.Identity != expected.Identity ||
		!expected.SamePicture(observed.Replay)) {
		return comparisonWitnessConflict()
	}
	return nil
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
	// Only transfer pins proven while matching the original row. Output rows are renumbered.
	applied := map[string]map[string]comparisonAppliedPair{}
	finish := func() (api.ComparisonPlan, error) {
		matched := applyComparisonMatches(plan, result, observations, options)
		for id, source := range retained.ComparisonSources {
			source.AppliedPairs = applied[id]
			retained.ComparisonSources[id] = source
		}
		return matched, retainMatchedComparisonWitnesses(matched, observations, retained)
	}
	defer func() {
		if len(result.Rows) > 0 && (errors.Is(matchErr, context.Canceled) || errors.Is(matchErr, context.DeadlineExceeded)) {
			var err error
			matchedPlan, err = finish()
			if err != nil {
				matchErr = err
			}
		}
	}()
	retainedBytes := int64(0)
	totalBytes := int64(24 << 30)
	budgets := map[string]*screenshots.ComparisonDecodeBudget{}
	columnsByID := make(map[string]int, len(plan.Sources))
	for i, source := range plan.Sources {
		columnsByID[source.ID] = i + 1
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
	durationFor := func(id string) float64 {
		source := retained.ComparisonSources[id].Source
		if source.VideoDurationSeconds > 0 {
			return source.VideoDurationSeconds
		}
		return source.DurationSeconds
	}
	inspectCrop := func(id string, row int, center, radius float64) error {
		if !canInspect {
			return nil
		}
		start := max(0, center-radius)
		span := min(4, durationFor(id)-start)
		workCtx, finishWindow := progress.decodeWindow(ctx, columnsByID[id], row)
		crop, err := detector.DetectComparisonActivePicture(workCtx, retained.ComparisonSources[id].Source, start, span, budgets[id])
		finishWindow(err)
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
	sample := func(id string, row int, center, radius float64, crop *screenshots.ComparisonCrop) ([]screenshots.ComparisonSample, error) {
		source := retained.ComparisonSources[id].Source
		start, end := max(0, center-radius), min(durationFor(id), center+radius)
		if end <= start || budgets[id].ConsumedMovieSeconds+end-start > budgets[id].MovieSecondsLimit {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		available := (128 << 20) - (16 << 20) - retainedBytes
		if available < 4096 {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		workCtx, finishWindow := progress.decodeWindow(ctx, columnsByID[id], row)
		frames, err := renderer.SampleComparisonSource(workCtx, screenshots.ComparisonSampleRequest{
			Renderer:         renderers[id],
			Retain:           screenshots.ComparisonRetentionLimit(available),
			Source:           source,
			StartSeconds:     start,
			DurationSeconds:  end - start,
			StrideSeconds:    .002,
			NativeFrames:     true,
			NormalizeSDR:     true,
			MatchCrop:        crop,
			MatchAspectRatio: matchAspect,
			Budget:           budgets[id],
		})
		finishWindow(err)
		retainedBytes += int64(len(frames)) * 4096
		if err != nil {
			return nil, fmt.Errorf("sample manual comparison: %w", err)
		}
		return orderedComparisonSamples(frames)
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
		mainSource := retained.ComparisonSources[plan.MainSourceID]
		expected, err := comparisonManualReplayAt(plan, mainSource, plan.MainSourceID, moment, moment)
		if err != nil {
			return plan, err
		}
		guardRequest := screenshots.ComparisonSampleRequest{
			Source:           mainSource.Source,
			Budget:           budgets[plan.MainSourceID],
			Retain:           screenshots.ComparisonRetentionLimit(available),
			MatchAspectRatio: matchAspect,
		}
		workCtx, finishWindow := progress.decodeWindow(ctx, columnsByID[plan.MainSourceID], momentIndex+1)
		if expected.CanReinspectDisc() {
			expected, err = renderer.ReinspectComparisonReplay(workCtx, guardRequest, expected)
			if err != nil {
				finishWindow(err)
				return plan, fmt.Errorf("reinspect saved comparison span: %w", err)
			}
		}
		guard, classified, err := b.inspectManualMainScene(workCtx, plan, retained, moment, guardRequest)
		finishWindow(err)
		if err != nil {
			return plan, err
		}
		if err := verifyManualComparisonWitness(expected, guard.ComparisonFrameObservation); err != nil {
			return plan, err
		}
		if err := inspectCrop(plan.MainSourceID, momentIndex+1, guard.RelativeTimestampSeconds, 2); err != nil {
			return plan, err
		}
		left, right := max(0, guard.RelativeTimestampSeconds-2), min(durationFor(plan.MainSourceID), guard.RelativeTimestampSeconds+2)
		var reference []screenshots.ComparisonSample
		if activePictures[plan.MainSourceID] == nil && left >= max(0, moment-2) && right <= min(durationFor(plan.MainSourceID), max(0, moment-2)+4) {
			for _, f := range classified {
				if f.RelativeTimestampSeconds >= left && f.RelativeTimestampSeconds < right {
					f.Evidence = nil
					reference = append(reference, f)
				}
			}
			retainedBytes += int64(len(reference)) * 4096
		} else {
			reference, err = sample(plan.MainSourceID, momentIndex+1, guard.RelativeTimestampSeconds, 2, activePictures[plan.MainSourceID])
			if err != nil {
				return plan, err
			}
		}
		selected := comparisonObservationID(guard.ComparisonFrameObservation)
		found := false
		for i, f := range reference {
			if comparisonObservationID(f.ComparisonFrameObservation) == selected {
				if !comparisonRecord(guard.ComparisonFrameObservation).matches(f.ComparisonFrameObservation) {
					return plan, comparisonWitnessConflict()
				}
				found = true
				reference[i].Evidence = guard.Evidence
				break
			}
		}
		if !found {
			return plan, errors.New("the inspected main picture is unavailable; inspect its native timestamp again")
		}
		pins := map[string]comparisonAppliedPair{}
		windows := map[string][]screenshots.ComparisonSample{}
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
				windows[source.ID] = reference
				continue
			}
			center := guard.RelativeTimestampSeconds
			duration := durationFor(source.ID)
			scale := comparisons.PlaybackScale{Numerator: 1, Denominator: 1}
			knownNeighborhood := false
			profile, err := b.comparisonCalibrationProfile(retained, plan, source.ID)
			if err != nil {
				return plan, err
			}
			secondary := retained.ComparisonSources[source.ID]
			if prediction, ok := secondary.Calibration.prediction(profile, secondary.Source); ok {
				center = prediction.Predict(center)
				knownNeighborhood = center >= 0 && center < duration
				scale = secondary.Calibration.Scale
			}
			for _, anchor := range plan.Anchors {
				if anchor.SourceID == source.ID &&
					(math.Abs(anchor.MainSeconds-moment) < 1e-6 || math.Abs(anchor.MainSeconds-guard.RelativeTimestampSeconds) < 1e-6) {
					center = anchor.SourceSeconds
					knownNeighborhood = true
					// A timestamp-only anchor predicts a window. Only scoped preview
					// handles applied to both cells of the same row can waive proof.
					for _, row := range plan.Rows {
						pair, ok := secondary.AppliedPairs[row.ID]
						if row.Excluded || !ok || !pair.Main.SamePicture(guard.Replay) {
							continue
						}
						other, err := comparisonManualReplayAt(
							api.ComparisonPlan{MainSourceID: plan.MainSourceID, Rows: []api.ComparisonRow{row}},
							secondary,
							source.ID,
							moment,
							center,
						)
						if err != nil {
							return plan, err
						}
						if pair.Secondary.SamePicture(other) {
							pins[source.ID] = pair
							columns[i].Anchors = append(
								columns[i].Anchors,
								comparisons.MatchAnchor{MainSeconds: guard.RelativeTimestampSeconds, SourceSeconds: anchor.SourceSeconds},
							)
							break
						}
					}
				}
			}
			if knownNeighborhood {
				if err := inspectCrop(source.ID, momentIndex+1, center, 2); err != nil {
					return plan, err
				}
			} else {
				delete(activePictures, source.ID)
			}
			target, err := sample(source.ID, momentIndex+1, center, 6, activePictures[source.ID])
			if err != nil {
				return plan, err
			}
			var intraProbe []screenshots.ComparisonSample
			if len(columns[i].Anchors) == 0 {
				discover := func(reference, target []screenshots.ComparisonSample) (comparisons.ConstantAlignment, string) {
					mainFrames, sourceFrames := comparisonSamples(reference), comparisonSamples(target)
					if alignment, reason := comparisons.DiscoverAlignment(mainFrames, sourceFrames); reason == "" {
						return alignment, ""
					}
					return comparisons.DiscoverPlaybackAlignment(mainFrames, sourceFrames, scale)
				}
				alignment, reason := discover(reference, target)
				if reason != "" && !knownNeighborhood {
					// Bootstrap crop only inside the candidate window just decoded,
					// using its same initialization point. This is not a selected
					// moment or authority; inspect that moment after native proof.
					if err := inspectCrop(source.ID, momentIndex+1, center, 6); err != nil {
						return plan, err
					}
					if activePictures[source.ID] != nil {
						target, err = sample(source.ID, momentIndex+1, center, 6, activePictures[source.ID])
						if err != nil {
							return plan, err
						}
						alignment, reason = discover(reference, target)
					}
				}
				if reason == "" && !knownNeighborhood {
					previousCrop := activePictures[source.ID]
					if err := inspectCrop(source.ID, momentIndex+1, alignment.Predict(guard.RelativeTimestampSeconds), 2); err != nil {
						return plan, err
					}
					crop := activePictures[source.ID]
					if (crop == nil) != (previousCrop == nil) || crop != nil && previousCrop != nil && *crop != *previousCrop {
						target, err = sample(source.ID, momentIndex+1, center, 6, activePictures[source.ID])
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
				if source.PreferBFrames && len(target) > 0 &&
					!slices.ContainsFunc(target, func(f screenshots.ComparisonSample) bool { return f.PictureType != "I" }) {
					at := alignment.Predict(guard.RelativeTimestampSeconds)
					columns[i].IntraOnly = comparisonIntraOnlyRegion(target, at, duration)
					if !columns[i].IntraOnly {
						// The extra native frames establish picture policy only. Keep
						// discovery and the chosen frame within the original window.
						radius := min(9, duration) / 2
						probeCenter := max(radius, min(at, duration-radius))
						beforeProbe := retainedBytes
						evidence, err := sample(source.ID, momentIndex+1, probeCenter, radius, activePictures[source.ID])
						retainedBytes = beforeProbe
						if err != nil && !errors.Is(err, screenshots.ErrComparisonFastInconclusive) {
							return plan, err
						}
						columns[i].IntraOnly = comparisonIntraOnlyRegion(evidence, at, duration)
						if columns[i].IntraOnly {
							intraProbe = evidence
						}
					}
				}
			}
			columns[i].Frames = comparisonManualTargetSamples(target, intraProbe)
			windows[source.ID] = target
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
		for rowIndex, row := range matched.Rows {
			selectedSamples := map[string][]screenshots.ComparisonSample{}
			retainComparisonRowSamples(selectedSamples, windows, row)
			for _, frame := range row.Frames {
				if frame.SourceID == plan.MainSourceID || frame.Method != "manual" {
					continue
				}
				for _, sample := range selectedSamples[frame.SourceID] {
					expected, err := comparisonManualReplayAt(plan, retained.ComparisonSources[frame.SourceID], frame.SourceID, moment, frame.TimestampSeconds)
					if err != nil {
						return plan, err
					}
					if err := verifyManualComparisonWitness(expected, sample.ComparisonFrameObservation); err != nil {
						return plan, err
					}
				}
			}
			for _, frame := range row.Frames {
				if pair, ok := pins[frame.SourceID]; ok && frame.Resolved && frame.Method == "manual" {
					if applied[frame.SourceID] == nil {
						applied[frame.SourceID] = map[string]comparisonAppliedPair{}
					}
					applied[frame.SourceID][strconv.Itoa(len(result.Rows)+rowIndex+1)] = pair
				}
			}
			for id, samples := range selectedSamples {
				observations[id] = append(observations[id], samples...)
			}
		}
		retainedBytes = 0
		for id, frames := range observations {
			ordered, err := orderedComparisonSamples(frames)
			if err != nil {
				return plan, err
			}
			observations[id] = ordered
			retainedBytes += int64(len(ordered)) * 4096
		}
		result.Rows = append(result.Rows, matched.Rows...)
		progress.update(momentIndex+1, len(options.MainMoments))
	}
	progress.complete()
	if options.DarkCount+options.MotionCount+options.FlatCount > 0 {
		result.Warnings = append(result.Warnings, "Explicitly selected pictures retain no inferred scene category; requested qualified targets remain unmet.")
	}
	return finish()
}

func comparisonManualTargetSamples(target, intraProbe []screenshots.ComparisonSample) []comparisons.FrameSample {
	frames := comparisonSamples(target)
	if intraProbe == nil {
		return frames
	}
	// Keep the visual neighborhood intact. Only a picture also observed in
	// the continuous all-I probe can use that probe's picture-type exception.
	observed := make(map[string]comparisonKnownPicture, len(intraProbe))
	for _, sample := range intraProbe {
		observed[sample.FrameID] = comparisonRecord(sample.ComparisonFrameObservation)
	}
	for i, sample := range target {
		picture, ok := observed[sample.FrameID]
		if !ok || !picture.matches(sample.ComparisonFrameObservation) {
			frames[i].Unselectable = true
		}
	}
	return frames
}
