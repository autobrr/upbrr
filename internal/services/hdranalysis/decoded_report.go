// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ProjectPictureReport adapts a verified picture report to the current
// public envelope without changing the stored checkpoint or measurement data.
func ProjectPictureReport(report *MeasurementReport, profile string) (*MeasurementReport, error) {
	if report == nil || report.Result.Mode == api.HDRDecodedAudio {
		return nil, errors.New("picture projection requires a picture report")
	}
	projected := *report
	switch profile {
	case api.HDRAnalysisProfileV3:
		if projected.Schema != "hdr-measurement-v2" {
			projected.Schema = "hdr-measurement-v2"
			setDecodedCoverage(&projected)
		}
	case api.HDRAnalysisProfileV2:
		projected.Schema = "hdr-measurement-v1"
		projected.Options.VideoDecoderThreads, projected.Options.AllAudioTracks = 0, false
		projected.Decoder.RequestedThreads, projected.Decoder.EffectiveThreads = 0, nil
		projected.Result.Coverage = ""
	default:
		return nil, errors.New("unsupported picture projection profile")
	}
	if err := projected.Validate(); err != nil {
		return nil, err
	}
	return &projected, nil
}

// Validate checks report schema, compatibility, finite values and bounded observations.
// Source and extraction fingerprints must still be verified by the resource owner.
func (r *MeasurementReport) Validate() error {
	if r != nil && r.Schema == "hdr-measurement-v2" {
		return r.validateV2()
	}
	if r != nil &&
		(r.Boundary != nil || r.AudioAuthority != nil || r.AreaSamples != nil || r.AreaIntervalsTotal != 0 || r.Options.RequiresV3() || r.Result.Coverage != "" || r.Result.AudioTracks != nil ||
			r.Decoder.RequestedThreads != 0 || len(r.Decoder.EffectiveThreads) != 0) {
		return errors.New("legacy measurement contains v2 authority")
	}
	if r == nil || r.Schema != "hdr-measurement-v1" || r.Dependency != NativeDependencyFingerprint ||
		len(r.Decoder.Version) > 200 || len(r.Decoder.Fingerprint) > 64 || len(r.Processing) > 3 || len(r.Selected) > 2 ||
		len(r.Scene) > 60 || len(r.Area) > 120 || len(r.Grade) > 120 || len(r.Thumbnails) > 24 ||
		len(r.Chromaticity[0]) > 4096 || len(r.Chromaticity[1]) > 4096 {
		return errors.New("invalid bounded HDR measurement report")
	}
	if err := r.Result.Validate(); err != nil {
		return fmt.Errorf("validate measurement outcome: %w", err)
	}
	if r.Automatic != nil {
		profile := AutomaticMeasurementProfile
		if r.Options.Sampled {
			profile = SampledMeasurementProfile
		}
		if r.Options.Sampled != (r.Automatic.Sampling != nil) || r.Result.Profile != profile {
			return errors.New("automatic sampling does not match requested measurement profile")
		}
		if r.Result.Mode != api.HDRDecodedAutomatic || len(r.Automatic.Frames) != r.Result.Measured || r.Result.Selected != r.Result.Measured {
			return errors.New("automatic measurement coverage differs from outcome")
		}
		if err := r.Automatic.validate(); err != nil {
			return err
		}
		if len(r.Automatic.Reference) > 0 {
			if err := validateAutomaticComparison(r); err != nil {
				return errors.New("retained automatic reference has invalid alignment")
			}
		}
		for _, frame := range r.Automatic.Frames {
			if frame.Borders[0]+frame.Borders[1] >= r.Raster[0] || frame.Borders[2]+frame.Borders[3] >= r.Raster[1] {
				return errors.New("automatic borders exceed the coded raster")
			}
		}
	} else if r.Result.Mode == api.HDRDecodedAutomatic && r.Result.Measured != 0 {
		return errors.New("automatic outcome has no retained pictures")
	}
	if r.Audio != nil {
		if r.Result.Mode != api.HDRDecodedAudio || r.Result.Measured != 1 {
			return errors.New("audio measurement differs from outcome")
		}
		if err := r.Audio.validate(); err != nil {
			return err
		}
	} else if r.Result.Mode == api.HDRDecodedAudio && r.Result.Measured != 0 {
		return errors.New("audio outcome has no retained evidence")
	}
	if r.Raster[0] < 0 || r.Raster[0] > 8192 || r.Raster[1] < 0 || r.Raster[1] > 4320 {
		return errors.New("measurement raster exceeds bounds")
	}
	if err := r.Options.Validate(); err != nil {
		return fmt.Errorf("validate measurement options: %w", err)
	}
	if !r.Options.Requested(r.Result.Mode) ||
		!slices.Contains(
			[]string{
				SceneMeasurementProfile,
				AreaMeasurementProfile,
				GradeMeasurementProfile,
				AutomaticMeasurementProfile,
				SampledMeasurementProfile,
				api.AudioAnalysisProfileVersion,
			},
			r.Result.Profile,
		) {
		return errors.New("measurement report does not match requested profile")
	}
	for i, source := range r.Sources {
		if len(source) > 512 || len(r.Tracks[i]) > 20 {
			return errors.New("measurement source identity exceeds bounds")
		}
	}
	for _, processing := range r.Processing {
		if len(processing) > 1024 {
			return errors.New("measurement processing description exceeds bounds")
		}
	}
	for _, selected := range r.Selected {
		if len(selected) > 600 {
			return errors.New("measurement selection exceeds bounds")
		}
		for i, index := range selected {
			if index < 0 || index >= MaxFrames || i > 0 && index <= selected[i-1] {
				return errors.New("invalid measurement selection")
			}
		}
	}
	if len(r.Selected) > 0 && len(r.Selected[0]) != r.Result.Selected {
		return errors.New("measurement selection count differs from outcome")
	}
	for _, thumbnail := range r.Thumbnails {
		if thumbnail.Index < 0 || thumbnail.Index >= MaxFrames || thumbnail.Candidate < 0 || thumbnail.Candidate >= MaxFrames ||
			thumbnail.Width != sceneWidth ||
			thumbnail.Height != sceneHeight ||
			len(thumbnail.Luma) != sceneWidth*sceneHeight || len(thumbnail.RGB) != 0 && len(thumbnail.RGB) != sceneWidth*sceneHeight*3 {
			return errors.New("invalid measurement thumbnail")
		}
	}
	for _, scene := range r.Scene {
		if err := validateSceneObservation(scene, MaxFrames); err != nil {
			return err
		}
	}
	for _, area := range r.Area {
		if err := validateAreaObservation(area, MaxFrames); err != nil {
			return err
		}
	}
	for i, grade := range r.Grade {
		if grade.IndexA < 0 || grade.IndexA >= MaxFrames || grade.IndexB < 0 || grade.IndexB >= MaxFrames ||
			i > 0 && (grade.IndexA <= r.Grade[i-1].IndexA || grade.IndexB <= r.Grade[i-1].IndexB) ||
			len(grade.PTSA.Value) > 21 || len(grade.PTSA.Timescale) > 20 || len(grade.PTSB.Value) > 21 || len(grade.PTSB.Timescale) > 20 {
			return errors.New("invalid comparison identity")
		}
		for _, stats := range []GradeFrameStatistics{grade.A, grade.B} {
			if stats.OriginalPixels < 4 || stats.OriginalPixels > 8192*4320 || stats.ReducedPixels != stats.OriginalPixels/4 ||
				stats.InvalidPixels < 0 || stats.InvalidPixels > stats.OriginalPixels || stats.RoundoffPixels < 0 || stats.RoundoffPixels > stats.OriginalPixels ||
				stats.ReducedInvalid < 0 || stats.ReducedInvalid > stats.ReducedPixels || !finiteWithin(stats.MaxRGBNits, 0, 10000) || !finiteWithin(stats.MeanYNits, 0, 10000.0001) {
				return errors.New("invalid comparison light statistics")
			}
		}
		if grade.Difference.EligiblePixels < 0 || grade.Difference.EligiblePixels > min(grade.A.ReducedPixels, grade.B.ReducedPixels) {
			return errors.New("invalid comparison coverage")
		}
		for channel := range 3 {
			if !finiteWithin(grade.Difference.SignedMeanPQ[channel], -1, 1) || !finiteWithin(grade.Difference.MeanAbsolutePQ[channel], 0, 1) ||
				!finiteWithin(grade.Difference.P95AbsolutePQ[channel], 0, 1) || !finiteWithin(grade.Difference.MaxAbsolutePQ[channel], 0, 1) {
				return errors.New("invalid comparison PQ statistics")
			}
		}
	}
	for _, points := range r.Chromaticity {
		for _, point := range points {
			if err := validateChromaticityPoint(point, MaxFrames, 8192*4320/4); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSceneObservation(scene SceneObservation, frames int) error {
	if scene.Candidate < 0 || scene.Candidate >= frames || scene.Segment < 0 || scene.Segment > 4 ||
		scene.BestOffset < -3 || scene.BestOffset > 3 || len(scene.PTS.Value) > 21 || len(scene.PTS.Timescale) > 20 ||
		!slices.Contains([]string{"unclear", "dominant_cut"}, scene.Confidence) || !finiteWithin(scene.Noise, 0, 1) {
		return errors.New("invalid scene observation")
	}
	for _, value := range scene.Scores {
		if !finiteWithin(value, 0, 1) {
			return errors.New("invalid scene score")
		}
	}
	return nil
}

func validateAreaObservation(area AreaObservation, frames int) error {
	if area.Start < 0 || area.End <= area.Start || area.End > frames || area.Samples < 0 || area.Samples > 3 ||
		!slices.Contains([]string{"unclear", "stable_border"}, area.Confidence) {
		return errors.New("invalid active-area observation")
	}
	for side := range 4 {
		// Impossible authored uint16 L5 offsets remain reportable evidence.
		if area.Expected[side] < 0 || area.Expected[side] > 65535 || area.Observed[side] < 0 || area.Observed[side] > 8192 ||
			area.Delta[side] < -65535 || area.Delta[side] > 8192 {
			return errors.New("invalid active-area offsets")
		}
	}
	return nil
}

func validateChromaticityPoint(point ChromaticityPoint, frames, pixels int) error {
	if point.Index < 0 || point.Index >= frames || point.Pixel < 0 || point.Pixel >= pixels ||
		!finiteWithin(point.X, 0, 1) || !finiteWithin(point.Y, 0, 1) || point.X+point.Y > 1.0000001 {
		return errors.New("invalid chromaticity point")
	}
	return nil
}

func finiteWithin(value, low, high float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= low && value <= high
}

// WriteMeasurement validates and writes a report within MaxMeasurementBytes.
// The caller owns the writer and atomic publication of any partial output.
func WriteMeasurement(ctx context.Context, output io.Writer, report *MeasurementReport) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write measurement: %w", err)
	}
	if err := report.Validate(); err != nil {
		return err
	}
	limit := measurementByteLimit(report.Schema, report.Result.Mode)
	if err := json.MarshalWrite(&boundedWriter{output: output, remaining: limit}, report); err != nil {
		return fmt.Errorf("write bounded measurement: %w", err)
	}
	return nil
}

// ReadMeasurement validates bounded strict JSON without reopening source media.
// The caller owns the reader and verification of the encoded report's stored hash.
func ReadMeasurement(ctx context.Context, input io.Reader) (*MeasurementReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read measurement: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(input, MaxAutomaticMeasurementBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded measurement: %w", err)
	}
	if len(data) > MaxAutomaticMeasurementBytes {
		return nil, errors.New("measurement report exceeds byte limit")
	}
	schema, mode, err := measurementSchema(ctx, data)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > measurementByteLimit(schema, mode) {
		return nil, errors.New("measurement exceeds profile byte limit")
	}
	if schema == "hdr-measurement-v2" && mode != api.HDRDecodedAutomatic {
		if err := preflightMeasurementV2(ctx, data, mode); err != nil {
			return nil, err
		}
		var report MeasurementReport
		if err := json.Unmarshal(data, &report, json.RejectUnknownMembers(true)); err != nil {
			return nil, fmt.Errorf("decode v2 measurement: %w", err)
		}
		if err := report.Validate(); err != nil {
			return nil, err
		}
		return &report, nil
	}
	// Preflight nesting and token count before allocating typed slices. The
	// retained-resource hash is checked separately by its owning workflow.
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	for tokens := 0; ; tokens++ {
		_, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("inspect measurement JSON: %w", err)
		}
		if tokens > 100000000 || decoder.StackDepth() > 16 {
			return nil, errors.New("measurement JSON exceeds allocation bounds")
		}
		if tokens%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("inspect measurement JSON: %w", err)
			}
		}
		pointer := string(decoder.StackPointer())
		for _, bound := range []struct {
			prefix string
			limit  int
		}{
			{"/automatic/frames/", MaxFrames}, {"/automatic/reference/", MaxFrames},
			{"/automatic/scenes/", 2}, {"/automatic/areas/", 120},
			{"/automatic/chromaticity/0/", 65536}, {"/automatic/chromaticity/1/", 65536},
			{"/automatic/scenes/0/observations/", 60}, {"/automatic/scenes/1/observations/", 60},
			{"/automatic/scenes/0/thumbnails/", 24}, {"/automatic/scenes/1/thumbnails/", 24},
			{"/audio/images/", 2}, {"/audio/track/artifacts/", 3},
			{"/scene/", 60}, {"/area/", 120}, {"/grade/", 120}, {"/thumbnails/", 24},
			{"/chromaticity/0/", 4096}, {"/chromaticity/1/", 4096},
			{"/selected/", 2}, {"/selected/0/", 600}, {"/selected/1/", 600}, {"/processing/", 3},
		} {
			if rest, found := strings.CutPrefix(pointer, bound.prefix); found {
				index, _, _ := strings.Cut(rest, "/")
				if n, err := strconv.Atoi(index); err == nil && n >= bound.limit {
					return nil, errors.New("measurement JSON exceeds observation bounds")
				}
			}
		}
	}
	var report MeasurementReport
	if err := json.Unmarshal(data, &report, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode measurement report: %w", err)
	}
	if report.Result.Mode != api.HDRDecodedAutomatic && report.Result.Mode != api.HDRDecodedAudio && len(data) > MaxMeasurementBytes {
		return nil, errors.New("sampled measurement report exceeds byte limit")
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return &report, nil
}
