// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const MaxMeasurementBytes = 8 << 20

// MeasurementReport retains bounded numeric evidence and reduced contact-sheet
// pixels. Source paths and subprocess diagnostics never enter this document.
type MeasurementReport struct {
	Boundary           *BoundaryReport        `json:"boundary,omitempty"`
	AudioAuthority     *HDRAudioAuthority     `json:"audioAuthority,omitempty"`
	AreaSamples        []BoundaryAreaSample   `json:"areaSamples,omitempty"`
	AreaIntervalsTotal int                    `json:"areaIntervalsTotal,omitempty"`
	Automatic          *AutomaticMeasurement  `json:"automatic,omitempty"`
	Audio              *AutomaticAudio        `json:"audio,omitempty"`
	Schema             string                 `json:"schema"`
	Result             api.HDRDecodedResult   `json:"result"`
	Decoder            DecoderIdentity        `json:"decoder"`
	Sources            [2]string              `json:"sources"`
	Tracks             [2]string              `json:"tracks"`
	Raster             [2]int                 `json:"raster"`
	Dependency         string                 `json:"dependency"`
	Options            api.HDRDecodedOptions  `json:"options"`
	Processing         []string               `json:"processing"`
	Selected           [][]int                `json:"selected"`
	Scene              []SceneObservation     `json:"scene,omitempty"`
	Area               []AreaObservation      `json:"area,omitempty"`
	Grade              []GradeObservation     `json:"grade,omitempty"`
	Chromaticity       [2][]ChromaticityPoint `json:"chromaticity"`
	Thumbnails         []MeasurementThumbnail `json:"thumbnails,omitempty"`
	RGBToXYZ           [3][3]float64          `json:"rgbToXYZ"`
	WhitePoint         [2]float64             `json:"whitePoint"`
	PQBinWidth         float64                `json:"pqBinWidth"`
}

func newMeasurementReport(mode api.HDRDecodedMode, primary DecodedInput, options api.HDRDecodedOptions) *MeasurementReport {
	profile := SceneMeasurementProfile
	switch mode {
	case api.HDRDecodedBoundarySync:
		profile = BoundaryMeasurementProfile
	case api.HDRDecodedAutomatic:
		profile = AutomaticMeasurementProfile
		if options.Sampled {
			profile = SampledMeasurementProfile
		}
	case api.HDRDecodedAudio:
		profile = api.AudioAnalysisProfileVersion
	case api.HDRDecodedSceneSync:
	case api.HDRDecodedActiveArea:
		profile = AreaMeasurementProfile
		if options.BoundarySync {
			profile = BoundaryAreaProfile
		}
	case api.HDRDecodedGrade, api.HDRDecodedColour:
		profile = GradeMeasurementProfile
	}
	report := &MeasurementReport{
		Schema:     "hdr-measurement-v1",
		Dependency: NativeDependencyFingerprint,
		Sources:    [2]string{primary.Fingerprint, ""},
		Tracks:     [2]string{decodedTrack(primary.Native), ""},
		Options:    options,
		Raster:     [2]int{primary.Source.Width, primary.Source.Height},
		Result: api.HDRDecodedResult{
			Mode:      mode,
			State:     api.HDRDecodedNotRun,
			Profile:   profile,
			Artifacts: []api.PublicResourceID{},
		},
	}
	if mode == api.HDRDecodedAutomatic {
		report.RGBToXYZ, report.WhitePoint = gradeRGBToXYZ, [2]float64{0.3127, 0.3290}
	}
	if options.RequiresV3() {
		report.Schema = "hdr-measurement-v2"
		report.Result.Coverage = api.HDRCoverageUnknown
	}
	return report
}

func decodedTrack(evidence *NativeEvidence) string {
	if evidence != nil {
		for _, result := range evidence.Results {
			if result.TrackID != "" {
				return result.TrackID
			}
		}
	}
	return ""
}

// decodedMemory reserves mapping arrays, bounded observations, all reduced
// thumbnails, numeric encoding, renderer/PNG scratch and the one raw frame.
// FFmpeg's own allocations are independently bounded by the process policy.
func decodedMemory(inputs []DecodedInput, otherBytes int64, frameBytes int) error {
	used := otherBytes + int64(frameBytes) + 128<<20
	for index, input := range inputs {
		if input.Native == nil {
			return errDecodedMapping
		}
		bytes, err := input.Native.RetainedBytes()
		if err != nil {
			return err
		}
		used += bytes
		if index == 0 {
			used += input.Native.otherRetainedBytes
		}
	}
	if otherBytes < 0 || used > MaxRestoreBytes {
		return errors.New("decoded measurement exceeds retained memory policy")
	}
	return nil
}

func measurementError(report *MeasurementReport, err error, capability bool) {
	report.Result.State, report.Result.Reason = api.HDRDecodedFailed, "decoder_failed"
	switch {
	case errors.Is(err, context.Canceled):
		report.Result.State, report.Result.Reason = api.HDRDecodedCanceled, "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		report.Result.Reason = "decoder_deadline"
	case capability || errors.Is(err, errDecodedMapping):
		report.Result.State, report.Result.Reason = api.HDRDecodedUnsupported, "unsupported_mapping_or_decoder"
	}
	// A failed traversal grants no measurement completion or image authority.
	report.Result.Measured = 0
	report.Scene, report.Area, report.Grade, report.Thumbnails = nil, nil, nil, nil
	report.Chromaticity = [2][]ChromaticityPoint{}
	report.Automatic, report.Audio = nil, nil
}

// MeasureDecoded performs optional bounded picture checks inside held admission.
// publish checkpoints one terminal mode before the next processing group starts.
// The caller checkpoints native evidence first and supplies only authorized paths.
func (s *Session) MeasureDecoded(ctx context.Context, primary DecodedInput, comparison *DecodedInput, options api.HDRDecodedOptions,
	otherRetainedBytes int64, publish func(*MeasurementReport) error,
) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := options.Validate(); err != nil {
		return fmt.Errorf("validate decoded analysis: %w", err)
	}
	if publish == nil {
		return errors.New("decoded analysis requires a measurement checkpoint")
	}
	executable, executableErr := screenshots.ResolveFFmpegExecutable()
	decoder := pictureDecoder{executable: executable}
	if options.SceneSync || options.ActiveArea {
		if err := s.measureSingle(ctx, decoder, executableErr, primary, options, otherRetainedBytes, publish); err != nil {
			return err
		}
	}
	if options.Comparison != nil {
		if err := s.measureComparison(ctx, decoder, executableErr, primary, comparison, options, otherRetainedBytes, publish); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) measureSingle(ctx context.Context, decoder pictureDecoder, capabilityErr error, primary DecodedInput,
	options api.HDRDecodedOptions, otherBytes int64, publish func(*MeasurementReport) error,
) error {
	var reports []*MeasurementReport
	for _, mode := range []api.HDRDecodedMode{api.HDRDecodedSceneSync, api.HDRDecodedActiveArea} {
		if options.Requested(mode) {
			reports = append(reports, newMeasurementReport(mode, primary, options))
		}
	}
	request := pictureDecodeRequest{inputs: []DecodedInput{primary}}
	var times []pictureTime
	if capabilityErr == nil {
		capabilityErr = decodedMemory(request.inputs, otherBytes, 0)
	}
	if capabilityErr == nil {
		times, capabilityErr = decodedTimeline(primary)
	}
	if capabilityErr != nil {
		for _, report := range reports {
			measurementError(report, capabilityErr, true)
			if err := publish(report); err != nil {
				return err
			}
		}
		return nil
	}
	var scene *sceneMeasurement
	var area *areaMeasurement
	var selected []int
	var areaErr error
	if options.SceneSync {
		scene = planSceneMeasurement(primary.Native.DolbyVision, times)
		selected = append(selected, scene.selected...)
	}
	if options.ActiveArea {
		area, areaErr = planAreaMeasurement(primary.Native.DolbyVision)
		if area != nil {
			selected = append(selected, area.selected...)
		}
	}
	slices.Sort(selected)
	selected = slices.Compact(selected)
	request.times, request.selected = [][]pictureTime{times}, [][]int{selected}
	request.threads = requestedVideoThreads(options.VideoDecoderThreads, 2, request.inputs)
	var identity DecoderIdentity
	var decodeErr error
	if len(selected) > 0 {
		frameBytes, geometryErr := request.geometry()
		capabilityErr = geometryErr
		if capabilityErr == nil {
			capabilityErr = decodedMemory(request.inputs, otherBytes, frameBytes)
		}
		if capabilityErr == nil {
			identity, capabilityErr = decoder.capabilities(ctx, false)
		}
		if capabilityErr == nil {
			s.service.progress(ctx, "measuring decoded scene and active area")
			decodeErr = decoder.decode(ctx, request, func(position int, frame []byte) error {
				index := selected[position]
				if scene != nil && slices.Contains(scene.selected, index) {
					scene.observe(index, reduceSceneLuma(frame, primary.Source.Width, primary.Source.Height))
				}
				if area != nil && slices.Contains(area.selected, index) {
					area.observe(index, frame, primary.Source.Width, primary.Source.Height)
				}
				return nil
			})
		}
	}
	for _, report := range reports {
		report.Decoder, report.Selected = identity, [][]int{}
		if report.Schema == "hdr-measurement-v2" {
			report.Decoder.RequestedThreads = options.VideoDecoderThreads
			if len(selected) > 0 && capabilityErr == nil {
				report.Decoder.EffectiveThreads = []int{request.threads}
			}
		}
		report.Processing = []string{decodedColourFilter(primary.Source, false), "coded-raster gray16le; scene cue: 192x108 exact-area nonlinear luma"}
		complete := false
		if report.Result.Mode == api.HDRDecodedSceneSync {
			report.Scene, report.Thumbnails, complete = scene.finish()
			report.Selected = [][]int{scene.selected}
		} else if areaErr == nil {
			report.Area, report.Thumbnails, complete = area.finish()
			report.Selected = [][]int{area.selected}
		}
		if len(report.Selected) != 0 {
			report.Result.Selected = len(report.Selected[0])
		}
		report.Result.Measured = report.Result.Selected
		report.Result.State, report.Result.Reason = api.HDRDecodedInconclusive, "insufficient_confident_evidence"
		if complete {
			report.Result.State, report.Result.Reason = api.HDRDecodedComplete, ""
		}
		switch {
		case report.Result.Mode == api.HDRDecodedActiveArea && areaErr != nil:
			measurementError(report, areaErr, true)
		case capabilityErr != nil:
			measurementError(report, capabilityErr, true)
		case decodeErr != nil:
			s.service.ReportFailure(ctx, "decoded", decodeErr)
			measurementError(report, decodeErr, false)
		}
		setDecodedCoverage(report)
		if err := publish(report); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) measureComparison(ctx context.Context, decoder pictureDecoder, capabilityErr error, primary DecodedInput, comparison *DecodedInput,
	options api.HDRDecodedOptions, otherBytes int64, publish func(*MeasurementReport) error,
) error {
	request := pictureDecodeRequest{inputs: []DecodedInput{primary}, crop: &options.Comparison.Crop}
	if comparison == nil {
		capabilityErr = errDecodedMapping
	} else {
		request.inputs = append(request.inputs, *comparison)
	}
	if capabilityErr == nil {
		capabilityErr = decodedMemory(request.inputs, otherBytes, 0)
	}
	if capabilityErr == nil {
		for _, input := range request.inputs {
			times, err := decodedTimeline(input)
			if err != nil {
				capabilityErr = err
				break
			}
			request.times = append(request.times, times)
		}
	}
	if capabilityErr == nil {
		var a, b []int
		a, b, capabilityErr = comparisonIndices(request.times[0], request.times[1], *options.Comparison.FrameOffset)
		request.selected = [][]int{a, b}
	}
	if capabilityErr == nil {
		frameBytes, err := request.geometry()
		capabilityErr = err
		if capabilityErr == nil {
			capabilityErr = decodedMemory(request.inputs, otherBytes, frameBytes)
		}
	}
	var identity DecoderIdentity
	if capabilityErr == nil {
		identity, capabilityErr = decoder.capabilities(ctx, true)
	}
	plan := &gradeMeasurement{}
	request.threads = requestedVideoThreads(options.VideoDecoderThreads, 2, request.inputs)
	if request.threads == 0 && capabilityErr == nil {
		capabilityErr = errors.New("paired video decoders exceed the CPU thread budget")
	}
	var decodeErr error
	if capabilityErr == nil {
		s.service.progress(ctx, "measuring aligned HDR base-layer pictures")
		decodeErr = decoder.decode(ctx, request, func(position int, frame []byte) error {
			plan.observe(position, request, frame)
			return nil
		})
	}
	for _, mode := range []api.HDRDecodedMode{api.HDRDecodedGrade, api.HDRDecodedColour} {
		if !options.Requested(mode) {
			continue
		}
		report := newMeasurementReport(mode, primary, options)
		report.Decoder, report.Selected = identity, request.selected
		if report.Schema == "hdr-measurement-v2" {
			report.Decoder.RequestedThreads = options.VideoDecoderThreads
			if capabilityErr == nil {
				for range request.inputs {
					report.Decoder.EffectiveThreads = append(report.Decoder.EffectiveThreads, request.threads)
				}
			}
		}
		if comparison != nil {
			report.Sources[1], report.Tracks[1] = comparison.Fingerprint, decodedTrack(comparison.Native)
			report.Processing = []string{decodedColourFilter(primary.Source, true), decodedColourFilter(comparison.Source, true),
				"common integer RGB crop; synthetic setpts=N pairing; float hstack; exact 2x2 linear average; HDR10-compatible base layer only"}
		}
		report.Grade, report.Chromaticity = plan.observations, plan.points
		report.RGBToXYZ, report.WhitePoint, report.PQBinWidth = gradeRGBToXYZ, [2]float64{0.3127, 0.3290}, 1.0/gradeHistogramBins
		if len(request.selected) > 0 {
			report.Result.Selected = len(request.selected[0])
		}
		report.Result.Measured = len(plan.observations)
		report.Result.State, report.Result.Reason = api.HDRDecodedComplete, ""
		for _, observation := range plan.observations {
			if !observation.Complete {
				report.Result.State, report.Result.Reason = api.HDRDecodedInconclusive, "invalid_rgb_or_insufficient_luminance"
			}
		}
		if capabilityErr != nil {
			measurementError(report, capabilityErr, true)
		} else if decodeErr != nil {
			s.service.ReportFailure(ctx, "decoded", decodeErr)
			measurementError(report, decodeErr, false)
		}
		setDecodedCoverage(report)
		if err := publish(report); err != nil {
			return err
		}
	}
	return nil
}

func setDecodedCoverage(report *MeasurementReport) {
	if report.Schema != "hdr-measurement-v2" {
		return
	}
	report.Result.Coverage = api.HDRCoverageUnknown
	if report.Result.Selected > 0 && report.Result.Measured == report.Result.Selected {
		report.Result.Coverage = api.HDRCoverageComplete
	} else if report.Result.Measured > 0 {
		report.Result.Coverage = api.HDRCoveragePartial
	}
}
