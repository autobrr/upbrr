// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// MeasureBoundary executes a previously planned bounded union. The caller must
// release native parser storage before supplying its remaining memory charge.
func (s *Session) MeasureBoundary(ctx context.Context, input DecodedInput, plan *BoundaryPlan, options api.HDRDecodedOptions, directory string,
	otherBytes int64, publish func(*MeasurementReport) error,
) error {
	if !options.BoundarySync || options.Validate() != nil || publish == nil || directory == "" {
		return errors.New("boundary analysis lacks prepared request authority")
	}
	unsupportedMapping := plan == nil
	if plan == nil {
		plan = &BoundaryPlan{unsupportedFormats: 1}
	}
	stageCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	boundary := newMeasurementReport(api.HDRDecodedBoundarySync, input, options)
	boundary.Boundary = &plan.report
	boundary.Processing = []string{"fixed numeric and ranked visual union; eight-frame PQ luma windows; metadata before/at image pairs",
		"finite compressed clips; complete VCL/NAL/SPS validation before software decoding; exact source/remux/decode PTS; no retries",
		"192x108 PQ luma scoring; selected pairs replayed as Reinhard SDR BT.709 colour previews, never brightness evidence"}
	var area *MeasurementReport
	if options.ActiveArea {
		area = newMeasurementReport(api.HDRDecodedActiveArea, input, options)
		area.Selected = [][]int{slices.Clone(plan.areaSelected)}
		area.AreaIntervalsTotal = plan.areaTotal
		area.Processing = []string{"coded full raster PQ luma; fixed start/midpoint/end and quarter/three-quarter samples",
			"initial three distinct samples determine border consensus; extra samples remain evidence, never override conflicting initial measurements"}
	}
	if otherBytes < 0 || otherBytes > MaxRestoreBytes-boundaryParentBytes-boundaryPreparationBytes {
		return errors.New("boundary preparation exceeds shared memory")
	}
	var executable string
	var capabilityErr error
	if unsupportedMapping {
		capabilityErr = errDecodedMapping
	} else {
		executable, capabilityErr = screenshots.ResolveFFmpegExecutable()
	}
	decoder := pictureDecoder{executable: executable}
	if capabilityErr == nil {
		boundary.Decoder, capabilityErr = decoder.capabilities(stageCtx, false)
		if capabilityErr == nil {
			version, err := decoder.probe(stageCtx, nil, "-hide_banner", "-version")
			if err != nil {
				capabilityErr = err
			} else if !strings.Contains(boundary.Decoder.Version, "a5923073bf") || !strings.Contains(string(version), "--arch=x86_64") {
				capabilityErr = errDecodedMapping
			}
		}
	}
	if area != nil {
		area.Decoder = boundary.Decoder
	}
	results := make([]boundaryWindowResult, len(plan.report.Windows))
	var slots chan struct{}
	var group sync.WaitGroup
	var geometry boundaryGeometry
	threads := 0
	var reservation int64
	stopReason := ""
	if capabilityErr == nil {
		for index, window := range plan.report.Windows {
			if slots != nil {
				select {
				case slots <- struct{}{}:
				case <-stageCtx.Done():
				}
			}
			if stageCtx.Err() != nil {
				stopReason = "stage_deadline"
				if ctx.Err() != nil {
					stopReason = "canceled"
				}
				break
			}
			s.service.reportProgress(
				ctx,
				Progress{Phase: "boundary_sync", Message: fmt.Sprintf("Preparing bounded picture window %d of %d", index+1, len(plan.report.Windows))},
			)
			path, proof, err := decoder.prepareBoundaryClip(stageCtx, input, plan, window, directory)
			prepare := BoundaryInvocation{
				RequestedThreads: options.VideoDecoderThreads,
				ReservedBytes:    boundaryPreparationBytes,
				State:            "unsupported",
			}
			if err != nil {
				plan.report.Windows[index].Reason = "clip_unavailable"
				plan.report.Windows[index].Invocations = []BoundaryInvocation{prepare}
				if slots != nil {
					<-slots
				}
				continue
			}
			prepare.State = "complete"
			prepare.ClipSHA256, prepare.CompressedBytes = proof.sha256, proof.bytes
			prepare.NALUnits = proof.nals
			plan.report.Windows[index].Invocations = []BoundaryInvocation{prepare}
			if slots == nil {
				var workers int
				workers, threads, reservation, err = boundaryConcurrency(proof.geometry, options.VideoDecoderThreads, otherBytes)
				if err == nil {
					geometry = proof.geometry
					slots = make(chan struct{}, workers)
					slots <- struct{}{}
				}
			} else if geometry != proof.geometry {
				err = errDecodedMapping
			}
			if err != nil {
				_ = os.Remove(path)
				plan.report.Windows[index].Reason = "unsupported_geometry_or_memory"
				if slots != nil {
					<-slots
				}
				continue
			}
			if proof.vcl > boundaryMaxVCL-plan.report.ChargedVCL {
				_ = os.Remove(path)
				<-slots
				plan.report.Windows[index].Reason = "decoded_picture_limit"
				stopReason = "decoded_picture_limit"
				break
			}
			// Permanently charged before launch, including failed/canceled decodes.
			plan.report.ChargedVCL += proof.vcl
			group.Go(func() {
				defer func() { _ = os.Remove(path); <-slots }()
				result := decoder.decodeBoundaryWindow(stageCtx, input, plan, window, path, proof, options.VideoDecoderThreads, threads, reservation)
				result.diagnostics = "" // Raw child diagnostics never enter retained evidence.
				results[index] = result
				plan.report.Windows[index].Invocations = append(plan.report.Windows[index].Invocations, result.invocation)
				if result.err != nil {
					plan.report.Windows[index].Reason = "window_unproved"
				}
			})
		}
	}
	group.Wait() // All children and scratch cleanup finish before publication or releasing admission.
	for index := range plan.report.Windows {
		window := &plan.report.Windows[index]
		if len(window.Invocations) == 0 {
			window.Reason = stopReason
			if capabilityErr != nil {
				window.Reason = "unsupported_sparse_decoder"
			}
		}
	}
	observations := make(map[int]SceneObservation)
	pairs := make(map[int][]MeasurementThumbnail)
	for _, result := range results {
		maps.Copy(observations, result.observations)
		maps.Copy(pairs, result.pairs)
		if area != nil {
			area.AreaSamples = append(area.AreaSamples, result.area...)
			area.Thumbnails = append(area.Thumbnails, result.areaThumbnails...)
		}
	}
	unclear := false
	for index := range plan.report.Formats {
		format := &plan.report.Formats[index]
		for _, candidate := range boundaryCandidates(*format) {
			if observation, ok := observations[candidate]; ok {
				format.Observations = append(format.Observations, observation)
				unclear = unclear || observation.Confidence == "unclear"
			}
		}
		format.Measured = len(format.Observations)
		format.Coverage = api.HDRCoveragePartial
		if format.Measured == format.Selected && format.Selected > 0 {
			format.Coverage = api.HDRCoverageComplete
		}
		if format.Measured == 0 {
			format.Coverage = api.HDRCoverageUnknown
		}
		for i := range format.VisualCandidates {
			candidate := format.VisualCandidates[i].Candidate
			for _, window := range plan.report.Windows {
				if candidate-4 >= window.First && candidate+3 <= window.Last && len(window.Invocations) > 1 {
					format.VisualCandidates[i].Attempted = true
					break
				}
			}
		}
		for stratum := range 12 {
			chosen := -1
			for _, visual := range format.VisualCandidates {
				observation, ok := observations[visual.Candidate]
				if visual.Stratum != stratum || !ok {
					continue
				}
				if chosen < 0 {
					chosen = visual.Candidate
				}
				if observation.Confidence == "dominant_cut" {
					chosen = visual.Candidate
					break
				}
			}
			if chosen >= 0 {
				format.Thumbnails = append(format.Thumbnails, pairs[chosen]...)
			}
		}
		boundary.Result.Selected += format.Selected
		boundary.Result.Measured += format.Measured
		unclear = unclear || format.MissingNumericStrata > 0 || format.MissingVisualStrata > 0
	}
	s.service.reportProgress(ctx, Progress{Phase: "boundary_previews", Message: "Rendering colour thumbnails for selected scene boundaries"})
	colourBoundaryPairs(stageCtx, decoder, input, plan, options, directory, otherBytes)
	finishBoundaryOutcome(
		&boundary.Result,
		boundary.Result.Measured == boundary.Result.Selected && plan.unsupportedFormats == 0,
		unclear,
		capabilityErr,
		ctx.Err(),
	)
	if stopReason != "" {
		boundary.Result.Reason = stopReason
	}
	if unsupportedMapping {
		boundary.Result.Reason = "unsupported_sparse_mapping"
	}
	boundary.Result.Checks = []api.HDRCheckResult{
		{
			Code:  "boundary_coverage",
			State: api.HDRCheckInconclusive,
			Message: fmt.Sprintf(
				"Measured %d of %d admitted boundary candidates. Numeric strata and ranked visual alternatives are reported separately; preview pairs remain at the metadata boundary.",
				boundary.Result.Measured,
				boundary.Result.Selected,
			),
		},
	}
	coloured, displayed := 0, 0
	for _, format := range plan.report.Formats {
		for _, thumb := range format.Thumbnails {
			displayed++
			if len(thumb.RGB) > 0 {
				coloured++
			}
		}
	}
	previewState := api.HDRCheckInconclusive
	if displayed > 0 && coloured == displayed {
		previewState = api.HDRCheckPass
	}
	boundary.Result.Checks = append(boundary.Result.Checks, api.HDRCheckResult{
		Code:    "boundary_previews",
		State:   previewState,
		Message: fmt.Sprintf("%d of %d displayed frames have SDR colour previews; unavailable previews retain grayscale luma cues.", coloured, displayed),
	})
	if err := publish(boundary); err != nil {
		return fmt.Errorf("publish boundary measurement: %w", err)
	}
	if area != nil {
		finishBoundaryArea(area, plan)
		finishBoundaryOutcome(&area.Result, len(area.AreaSamples) == len(plan.areaSelected) && len(plan.areas) == plan.areaTotal,
			slices.ContainsFunc(area.Area, func(observation AreaObservation) bool { return observation.Confidence == "unclear" }), capabilityErr, ctx.Err())
		if stopReason != "" {
			area.Result.Reason = stopReason
		}
		if unsupportedMapping {
			area.Result.Reason = "unsupported_sparse_mapping"
		}
		if err := publish(area); err != nil {
			return fmt.Errorf("publish boundary area: %w", err)
		}
	}
	return nil
}

func finishBoundaryOutcome(result *api.HDRDecodedResult, complete, unclear bool, capabilityErr, canceled error) {
	result.State, result.Coverage = api.HDRDecodedInconclusive, api.HDRCoveragePartial
	if complete && result.Selected > 0 {
		result.Coverage = api.HDRCoverageComplete
		if !unclear {
			result.State = api.HDRDecodedComplete
		}
	}
	if result.Measured == 0 {
		result.State, result.Coverage, result.Reason = api.HDRDecodedUnsupported, api.HDRCoverageUnknown, "no_proved_windows"
	}
	if capabilityErr != nil {
		result.Reason = "unsupported_sparse_decoder"
	}
	if canceled != nil {
		result.State, result.Reason = api.HDRDecodedCanceled, "canceled"
	}
}

func finishBoundaryArea(report *MeasurementReport, plan *BoundaryPlan) {
	slices.SortFunc(report.AreaSamples, func(a, b BoundaryAreaSample) int { return a.Index - b.Index })
	report.Result.Selected, report.Result.Measured = len(plan.areaSelected), len(report.AreaSamples)
	for _, interval := range plan.areas {
		observation := interval.AreaObservation
		var samples []BoundaryAreaSample
		for _, sample := range report.AreaSamples {
			if slices.Contains(interval.initial, sample.Index) {
				samples = append(samples, sample)
			}
		}
		observation.Samples = len(samples)
		confident := interval.Known && len(samples) == len(interval.initial) && len(samples) > 0
		for _, sample := range samples {
			confident = confident && sample.BrightEnough
		}
		for side := range 4 {
			values := make([]int, 0, len(samples))
			for _, sample := range samples {
				values = append(values, sample.Borders[side])
			}
			if len(values) > 0 {
				slices.Sort(values)
				observation.Observed[side] = values[len(values)/2]
				observation.Delta[side] = observation.Observed[side] - observation.Expected[side]
				confident = confident && values[len(values)-1]-values[0] <= 1
			}
		}
		confident = confident && observation.Expected[0]+observation.Expected[1] < report.Raster[0] &&
			observation.Expected[2]+observation.Expected[3] < report.Raster[1]
		if confident {
			observation.Confidence = "stable_border"
		}
		report.Area = append(report.Area, observation)
	}
}
