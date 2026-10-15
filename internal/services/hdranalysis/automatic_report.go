// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"slices"
	"strconv"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

const MaxAutomaticMeasurementBytes = 512 << 20

type AutomaticAudioImage struct {
	Kind   api.HDRArtifactKind `json:"kind"`
	Width  int                 `json:"width"`
	Height int                 `json:"height"`
	Data   []byte              `json:"data"`
}

// AutomaticAudio retains the original audio processor's exact published
// images and statistics, rather than approximating or recomputing its DSP.
type AutomaticAudio struct {
	Track  api.AudioAnalysisTrackResult `json:"track"`
	Images []AutomaticAudioImage        `json:"images"`
	Stats  string                       `json:"stats"`
}

func captureAutomaticAudio(report *MeasurementReport, tracks []audioanalysis.TrackResult) error {
	if len(tracks) == 0 {
		report.Result.State, report.Result.Reason = api.HDRDecodedNotRun, "no_audio"
		return nil
	}
	if len(tracks) != 1 {
		return errors.New("automatic audio must retain one primary track")
	}
	track := tracks[0]
	if report.AudioAuthority != nil && !track.PCMComplete || track.Public.Status == api.StageStatusFailed && report.AudioAuthority == nil {
		report.Result.State, report.Result.Reason = api.HDRDecodedFailed, "audio_analysis_failed"
		if report.AudioAuthority != nil {
			report.Audio = &AutomaticAudio{Track: track.Public}
			report.Audio.Track.SampleFrames = 0
			report.Audio.Track.Duration = 0
			report.Audio.Track.Artifacts = nil
			if track.Public.Status == api.StageStatusCanceled {
				report.Result.State, report.Result.Reason = api.HDRDecodedCanceled, "canceled"
			}
			if track.Public.Status == api.StageStatusInterrupted {
				report.Result.State, report.Result.Reason = api.HDRDecodedInterrupted, "interrupted"
			}
		}
		if report.AudioAuthority != nil && track.Public.Failure != nil &&
			(track.Public.Failure.Code == api.AudioAnalysisFailureUnsupportedLayout || track.Public.Failure.Code == api.AudioAnalysisFailureUnsupportedCodec) {
			report.Result.State, report.Result.Reason = api.HDRDecodedUnsupported, string(track.Public.Failure.Code)
		}
		return nil
	}
	report.Audio = &AutomaticAudio{Track: track.Public}
	for _, artifact := range track.Artifacts {
		if artifact.Public.Variant == api.AudioAnalysisStats {
			report.Audio.Stats = artifact.Public.Text
			continue
		}
		file, err := os.Open(artifact.Path)
		if err != nil {
			return fmt.Errorf("open shared audio image: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 32<<20+1))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return fmt.Errorf("retain shared audio image: %w", err)
		}
		if len(data) > 32<<20 {
			return errors.New("shared audio image exceeds its limit")
		}
		kind := api.HDRArtifactAudioWaveform
		if artifact.Public.Variant == api.AudioAnalysisSpectrogram {
			kind = api.HDRArtifactAudioSpectrogram
		}
		report.Audio.Images = append(report.Audio.Images, AutomaticAudioImage{
			Kind:   kind,
			Width:  artifact.Public.Width,
			Height: artifact.Public.Height,
			Data:   data,
		})
	}
	report.Result.State = api.HDRDecodedComplete
	if track.Public.Status != api.StageStatusCompleted {
		report.Result.State = api.HDRDecodedInconclusive
	}
	report.Result.Selected, report.Result.Measured = 1, 1
	code, label := "audio_primary_track", "Primary audio"
	if report.AudioAuthority != nil {
		code, label = "audio_prepared_track", fmt.Sprintf("Audio track %d", track.Public.Ordinal)
	}
	report.Result.Checks = []api.HDRCheckResult{
		{
			Code:  code,
			State: api.HDRCheckPass,
			Message: fmt.Sprintf(
				"%s: %d channels at %d Hz; %d sample frames. Existing waveform, SoX spectrogram and amplitude-statistics processing was used.",
				label,
				track.Public.Channels,
				track.Public.SampleRate,
				track.Public.SampleFrames,
			),
		},
	}
	return report.Audio.validate()
}

func (a *AutomaticAudio) validate() error {
	if a.Track.Channels < 1 || a.Track.Channels > 8 || a.Track.SampleRate < 1 || a.Track.SampleRate > 768000 ||
		a.Track.SampleFrames <= 0 || !finiteWithin(a.Track.Duration, 0, 24*60*60) ||
		math.Abs(a.Track.Duration-float64(a.Track.SampleFrames)/float64(a.Track.SampleRate)) > 1/float64(a.Track.SampleRate) ||
		len(a.Stats) > api.AudioAnalysisStatsMaxBytes || len(a.Images) > 2 || len(a.Track.Artifacts) > 3 ||
		len(a.Track.TrackID) > 128 || len(a.Track.Title) > 1024 || len(a.Track.Codec) > 128 || len(a.Track.ChannelLayout) > 128 {
		return errors.New("automatic audio exceeds bounds")
	}
	seen := make(map[api.HDRArtifactKind]bool)
	for _, image := range a.Images {
		if seen[image.Kind] || !slices.Contains([]api.HDRArtifactKind{api.HDRArtifactAudioWaveform, api.HDRArtifactAudioSpectrogram}, image.Kind) ||
			image.Width < 1 || image.Width > 8192 || image.Height < 1 || image.Height > 8192 || len(image.Data) == 0 || len(image.Data) > 32<<20 {
			return errors.New("invalid retained audio image")
		}
		seen[image.Kind] = true
		configuration, err := png.DecodeConfig(bytes.NewReader(image.Data))
		if err != nil || configuration.Width != image.Width || configuration.Height != image.Height {
			return errors.New("retained audio image geometry differs")
		}
	}
	return nil
}

func finishAutomaticReport(ctx context.Context, report *MeasurementReport, native *NativeExtraction, pictures *automaticPictures) error {
	// Borrow the completed parser model while its capture remains caller-owned.
	// No metadata storage is released or transferred while deriving picture checks.
	evidence := &NativeEvidence{HDR10Plus: native.HDR10Plus}
	if native.DolbyVision != nil {
		evidence.DolbyVision = &dovi.Analysis{Pictures: native.DolbyVision.Pictures, Records: make([]dovi.RPU, len(native.DolbyVision.Records))}
		for i, record := range native.DolbyVision.Records {
			evidence.DolbyVision.Records[i] = record.RPU
		}
	}
	frames := report.Automatic.Frames
	for i := range frames {
		if dm := sceneDM(evidence.DolbyVision, automaticFrameNumber(report, i, false)); dm != nil && dm.L1 != nil {
			frames[i].DVKnown = true
			frames[i].DVPQ = [2]float64{float64(dm.L1.MaxPQ) * 1023 / 4095, float64(dm.L1.AvgPQ) * 1023 / 4095}
		}
	}
	if evidence.HDR10Plus != nil {
		model, err := evidence.HDR10Plus.PlotMetadata()
		if err != nil {
			return fmt.Errorf("derive automatic HDR10+ overlay: %w", err)
		}
		for i := range frames {
			index := automaticFrameNumber(report, i, false)
			if index >= len(model.Frames) {
				return errDecodedMapping
			}
			value := model.Frames[index]
			peak, average := float64(slices.Max(value.MaxSCL))/10, float64(value.AverageRGB)/10
			maximumPQ, me := dovi.NitsToPQ(peak)
			averagePQ, ae := dovi.NitsToPQ(average)
			if me != nil || ae != nil {
				return errors.New("HDR10+ overlay values exceed absolute-light bounds")
			}
			frames[i].HDR10PlusKnown = true
			frames[i].HDR10PlusPQ = [2]float64{maximumPQ * 1023, averagePQ * 1023}
		}
		if pictures.preview != nil {
			for i, value := range model.Frames {
				peak, average := float64(slices.Max(value.MaxSCL))/10, float64(value.AverageRGB)/10
				maximumPQ, me := dovi.NitsToPQ(peak)
				averagePQ, ae := dovi.NitsToPQ(average)
				if me != nil || ae != nil || i >= len(pictures.preview.frames) {
					return errDecodedMapping
				}
				pictures.preview.frames[i].HDR10PlusKnown = true
				pictures.preview.frames[i].HDR10PlusPQ = [2]float64{maximumPQ * 1023, averagePQ * 1023}
			}
		}
	}
	knownBorders := 0
	var peak, average float64
	for _, frame := range frames {
		peak = max(peak, frame.MaxRGBNits)
		average += frame.FALLNits
		if frame.BorderKnown {
			knownBorders++
		}
	}
	cropDescription := "observed active picture, with the coded raster retained for ambiguous frames"
	if pictures.crop != nil {
		cropDescription = fmt.Sprintf("explicit common crop left=%d top=%d width=%d height=%d",
			pictures.crop.Left, pictures.crop.Top, pictures.crop.Width, pictures.crop.Height)
	}
	coverageDescription := fmt.Sprintf("all %d presentation frames", len(frames))
	if sampling := report.Automatic.Sampling; sampling != nil {
		coverageDescription = fmt.Sprintf(
			"%d selected pictures of %d presentation frames, every %d source frames",
			len(frames),
			sampling.Frames[0],
			sampling.Step,
		)
		report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
			Code:    "sampled_picture_coverage",
			State:   api.HDRCheckInconclusive,
			Message: "Brightness peaks, averages and differences describe measured samples. Short peaks or mismatches between samples can be missed; full-frame measurement remains available.",
		})
	}
	report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
		Code:  "complete_picture_measurement",
		State: api.HDRCheckPass,
		Message: fmt.Sprintf(
			"Measured %s using the %s and complete 2x2 linear-light footprints. Highest measured MaxRGB %.3f nits; mean measured FALL %.3f nits.",
			coverageDescription, cropDescription, peak, average/float64(len(frames)),
		),
	},
		api.HDRCheckResult{
			Code:    "metadata_metric_definitions",
			State:   api.HDRCheckInconclusive,
			Message: "DV L1 and HDR10+ MaxSCL/average RGB are metadata measurements with different definitions. Their overlays and differences are evidence, not independent proof of an identical picture grade.",
		})
	if knownBorders < len(frames) && pictures.crop == nil {
		report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
			Code:  "active_picture_coverage",
			State: api.HDRCheckInconclusive,
			Message: fmt.Sprintf(
				"Borders were measurable on %d of %d measured pictures. The remaining measured pictures use the full coded raster; their FALL can include bars.",
				knownBorders,
				len(frames),
			),
		})
	}
	for _, format := range []api.HDRNativeFormat{api.HDRNativeDolbyVision, api.HDRNativeHDR10Plus} {
		var differences []float64
		for _, frame := range frames {
			if format == api.HDRNativeDolbyVision && frame.DVKnown {
				differences = append(differences, frame.DVPQ[1]-picturePQ(frame.FALLNits))
			} else if format == api.HDRNativeHDR10Plus && frame.HDR10PlusKnown {
				differences = append(differences, frame.HDR10PlusPQ[1]-picturePQ(frame.FALLNits))
			}
		}
		if len(differences) > 0 {
			slices.Sort(differences)
			report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
				Code:  string(format) + "_average_difference",
				State: api.HDRCheckInconclusive,
				Message: fmt.Sprintf(
					"Metadata average minus picture FALL in 10-bit PQ codes: p5 %.3f, median %.3f, p95 %.3f (%d mapped measured frames, including near-black). Order statistics use lower ranks; plots exclude FALL <= 0.05 nits and interpolate percentiles. Metadata and picture statistics have different definitions.",
					differences[(len(differences)-1)*5/100],
					differences[(len(differences)-1)/2],
					differences[(len(differences)-1)*95/100],
					len(differences),
				),
			})
		}
	}
	cutPictures := pictures
	if pictures.preview != nil {
		cutPictures = pictures.preview
	}
	cutFrames := frames
	if pictures.preview != nil {
		cutFrames = pictures.preview.frames
	}
	times := make([]pictureTime, len(cutFrames))
	for i, frame := range cutFrames {
		var err error
		times[i], err = automaticTime(frame.PTS)
		if err != nil {
			return err
		}
	}
	for _, format := range []api.HDRNativeFormat{api.HDRNativeDolbyVision, api.HDRNativeHDR10Plus} {
		analysis := evidence.DolbyVision
		if format == api.HDRNativeHDR10Plus {
			analysis = hdr10PlusSceneAnalysis(cutFrames)
			if evidence.HDR10Plus != nil {
				for _, index := range evidence.HDR10Plus.SceneStarts {
					if index < uint64(len(analysis.Records)) && analysis.Records[index].DM != nil {
						analysis.Records[index].DM.SceneRefresh = 1
					}
				}
			}
		}
		plan := planSceneMeasurement(analysis, times)
		if len(plan.candidates) == 0 {
			continue
		}
		scene := automaticSceneResult(format, plan, cutFrames, cutPictures.cuts)
		report.Automatic.Scenes = append(report.Automatic.Scenes, scene)
		inSync, offset, unclear := 0, 0, 0
		for _, observation := range scene.Observations {
			switch {
			case observation.Confidence != "dominant_cut":
				unclear++
			case observation.BestOffset == 0:
				inSync++
			default:
				offset++
			}
		}
		state := api.HDRCheckPass
		if offset > 0 {
			state = api.HDRCheckWarning
		} else if inSync == 0 {
			state = api.HDRCheckInconclusive
		}
		report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
			Code:  string(format) + "_picture_sync",
			State: state,
			Message: fmt.Sprintf(
				"%s scene check: %d in sync, %d with an estimated offset, %d unclear; %d retained before/at-cut image pairs.",
				format,
				inSync,
				offset,
				unclear,
				len(scene.Thumbnails)/2,
			),
		})
	}
	if evidence.DolbyVision != nil {
		pairs := make(map[[2]float64]bool)
		minimumMax, minimumAverage, masteringLimit := 10000.0, 10000.0, 0
		for i := range evidence.DolbyVision.Pictures {
			dm := sceneDM(evidence.DolbyVision, i)
			if dm != nil && dm.L1 != nil {
				pairs[[2]float64{float64(dm.L1.MaxPQ), float64(dm.L1.AvgPQ)}] = true
				maximum, _ := dovi.PQToNits(float64(dm.L1.MaxPQ) / 4095)
				average, _ := dovi.PQToNits(float64(dm.L1.AvgPQ) / 4095)
				minimumMax, minimumAverage = min(minimumMax, maximum), min(minimumAverage, average)
			}
			if dm != nil && dm.L6 != nil {
				masteringLimit = max(masteringLimit, int(dm.L6.MaxMastering))
			}
		}
		if len(pairs) > 0 {
			state := api.HDRCheckInconclusive
			if len(pairs) <= 3 {
				state = api.HDRCheckWarning
			}
			report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
				Code:  "l1_range_and_floors",
				State: state,
				Message: fmt.Sprintf(
					"DV L1 has %d distinct maximum/average pairs; lowest maximum %.3f nits and average %.3f nits. Few values or elevated floors are review cues, not proof of an incorrect grade.",
					len(pairs),
					minimumMax,
					minimumAverage,
				),
			})
		}
		if masteringLimit > 0 {
			state := api.HDRCheckInconclusive
			if peak > float64(masteringLimit)*1.02 {
				state = api.HDRCheckWarning
			}
			report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
				Code:  "l6_mastering_picture_peak",
				State: state,
				Message: fmt.Sprintf(
					"Highest authored L6 mastering maximum %d nits; highest measured picture MaxRGB %.3f nits. Compare the source mastering context when reviewing this difference.",
					masteringLimit,
					peak,
				),
			})
		}
		area, areaErr := planAreaMeasurement(evidence.DolbyVision)
		if areaErr == nil {
			for _, interval := range area.intervals {
				observation := interval.AreaObservation
				observation.Confidence = "unclear"
				var borders [][4]int
				var candidates []AutomaticFrame
				for index, frame := range frames {
					number := automaticFrameNumber(report, index, false)
					if number >= observation.Start && number < observation.End {
						candidates = append(candidates, frame)
					}
				}
				selected := 0
				if len(candidates) > 0 {
					indices := slices.Compact([]int{0, (len(candidates) - 1) / 2, len(candidates) - 1})
					for _, index := range indices {
						frame := candidates[index]
						selected++
						if frame.BorderKnown {
							borders = append(borders, frame.Borders)
						}
					}
				}
				observation.Samples = len(borders)
				if len(borders) > 0 {
					observation.Observed = borders[0]
					if observation.Known && len(borders) == selected && !slices.ContainsFunc(borders, func(value [4]int) bool { return value != borders[0] }) {
						observation.Confidence = "stable_border"
					}
					for side := range 4 {
						observation.Delta[side] = observation.Observed[side] - observation.Expected[side]
					}
				}
				report.Automatic.Areas = append(report.Automatic.Areas, observation)
			}
		}
		mismatch, known := 0, 0
		for i, frame := range frames {
			expected, supplied := offsetsAt(evidence.DolbyVision, automaticFrameNumber(report, i, false))
			if supplied && frame.BorderKnown {
				known++
				if expected != frame.Borders {
					mismatch++
				}
			}
		}
		state := api.HDRCheckPass
		if mismatch > 0 {
			state = api.HDRCheckWarning
		} else if known == 0 {
			state = api.HDRCheckInconclusive
		}
		report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
			Code:  "l5_picture_borders",
			State: state,
			Message: fmt.Sprintf(
				"L5 versus coded-raster picture borders: %d measurable frames, %d different offsets. Dark or ambiguous borders remain unclear.",
				known,
				mismatch,
			),
		})
	}
	if report.Automatic.Profile5 && len(report.Automatic.Reference) == 0 {
		report.Result.Checks = append(report.Result.Checks, optionalHDR10ReferenceNote())
	}
	if len(report.Automatic.Reference) > 0 {
		if err := validateAutomaticComparison(report); err != nil {
			report.OmitAutomaticReference()
			report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
				Code:    "reference_alignment",
				State:   api.HDRCheckUnsupported,
				Message: "The reference does not have a verifiable matching frame cadence, crop and overlap. Its grade comparison was not published.",
			})
		} else {
			report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
				Code:    "reference_alignment",
				State:   api.HDRCheckPass,
				Message: "The selected HDR10 reference uses the explicit source-frame offset and common crop; paired measured brightness differences and colour evidence are available.",
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("finish automatic measurement: %w", err)
	}
	return report.Automatic.validate()
}

// OmitAutomaticReference removes unverifiable secondary evidence while
// preserving supported primary checks and the independently retained audio.
func (r *MeasurementReport) OmitAutomaticReference() {
	if r.Result.Mode != api.HDRDecodedAutomatic {
		return
	}
	r.Sources[1], r.Tracks[1] = "", ""
	if r.Automatic != nil {
		r.Automatic.Reference, r.Automatic.Chromaticity[1] = nil, nil
		if sampling := r.Automatic.Sampling; sampling != nil {
			sampling.Frames[1], sampling.Origins[1], sampling.Cadence = 0, MeasurementTimestamp{}, nil
		}
	}
	r.Result.Checks = slices.DeleteFunc(r.Result.Checks, func(check api.HDRCheckResult) bool {
		return check.Code == "reference_alignment" || check.Code == "reference_changed"
	})
	r.Result.Checks = append(r.Result.Checks, api.HDRCheckResult{
		Code:    "reference_changed",
		State:   api.HDRCheckUnsupported,
		Message: "The optional HDR10 reference is unavailable or changed. Its comparisons were omitted; supported primary-source checks remain available.",
	})
	if r.Automatic != nil && r.Automatic.Profile5 &&
		!slices.ContainsFunc(r.Result.Checks, func(check api.HDRCheckResult) bool { return check.Code == "optional_hdr10_reference" }) {
		r.Result.Checks = append(r.Result.Checks, optionalHDR10ReferenceNote())
	}
}

func hdr10PlusSceneAnalysis(frames []AutomaticFrame) *dovi.Analysis {
	a := &dovi.Analysis{Pictures: make([]dovi.Picture, len(frames)), Records: make([]dovi.RPU, len(frames))}
	for i, frame := range frames {
		if !frame.HDR10PlusKnown {
			continue
		}
		a.Pictures[i].HasRPU, a.Pictures[i].RPUIndex = true, uint32(i)
		a.Records[i].DM = &dovi.DM{
			L1: &dovi.L1{MaxPQ: uint16(math.Round(frame.HDR10PlusPQ[0] * 4095 / 1023)), AvgPQ: uint16(math.Round(frame.HDR10PlusPQ[1] * 4095 / 1023))},
		}
	}
	return a
}

func automaticSceneResult(format api.HDRNativeFormat, plan *sceneMeasurement, frames []AutomaticFrame, cuts []cachedCut) AutomaticScene {
	for i := range plan.candidates {
		candidate := &plan.candidates[i]
		for delta := -3; delta <= 3; delta++ {
			index := candidate.Candidate + delta
			if index < 1 || index >= len(frames) {
				candidate.usable = false
				continue
			}
			candidate.Scores[delta+3] = frames[index].SceneScore
			if frames[index].SceneScore > candidate.best {
				candidate.best = frames[index].SceneScore
				candidate.bright = frames[index].Bright
			}
		}
		candidate.pair = -1 // Online retained pictures, rather than post-scan decoder selections.
	}
	observations, _, _ := plan.finish()
	scene := AutomaticScene{Format: format, Observations: observations}
	for _, cut := range cuts {
		for _, observation := range observations {
			if observation.Confidence != "dominant_cut" || cut.index != observation.Candidate+observation.BestOffset || len(scene.Thumbnails) >= 24 {
				continue
			}
			pair := make([]MeasurementThumbnail, 0, 2)
			for _, thumbnail := range cut.frames {
				if thumbnail.Index == observation.Candidate-1 || thumbnail.Index == observation.Candidate {
					thumbnail.Candidate = observation.Candidate
					pair = append(pair, thumbnail)
				}
			}
			if len(pair) == 2 {
				scene.Thumbnails = append(scene.Thumbnails, pair...)
			}
			break
		}
	}
	return scene
}

func validateAutomaticComparison(report *MeasurementReport) error {
	if report.Options.Comparison == nil || report.Options.Comparison.FrameOffset == nil || report.Sources[1] == "" || len(report.Automatic.Reference) == 0 {
		return errDecodedMapping
	}
	if report.Automatic.Sampling != nil {
		return validateAutomaticSampleComparison(report)
	}
	times := [2][]pictureTime{}
	for source, frames := range [][]AutomaticFrame{report.Automatic.Frames, report.Automatic.Reference} {
		for _, frame := range frames {
			stamp, err := automaticTime(frame.PTS)
			if err != nil {
				return err
			}
			times[source] = append(times[source], stamp)
		}
	}
	_, _, err := comparisonIndices(times[0], times[1], *report.Options.Comparison.FrameOffset)
	return err
}

func automaticTime(stamp MeasurementTimestamp) (pictureTime, error) {
	if !stamp.Known || len(stamp.Value) > 21 || len(stamp.Timescale) > 20 {
		return pictureTime{}, errDecodedMapping
	}
	value, ve := strconv.ParseInt(stamp.Value, 10, 64)
	scale, se := strconv.ParseUint(stamp.Timescale, 10, 64)
	if ve != nil || se != nil || scale == 0 {
		return pictureTime{}, errDecodedMapping
	}
	return pictureTime{
		Value:     value,
		Timescale: scale,
		Valid:     true,
	}, nil
}
