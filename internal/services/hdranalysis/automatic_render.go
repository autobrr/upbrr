// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"slices"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

// MeasurementArtifacts enumerates outputs that the retained report can render.
func MeasurementArtifacts(report *MeasurementReport) []api.HDRTypedArtifact {
	if report == nil || report.Result.Measured == 0 {
		return nil
	}
	artifact := func(kind api.HDRArtifactKind) api.HDRTypedArtifact {
		width, height := kind.ImageDimensions()
		profile := api.HDRAnalysisProfileV2
		if report.Schema == "hdr-measurement-v2" {
			profile = api.HDRAnalysisProfileV3
		}
		return api.HDRTypedArtifact{
			Kind:    kind,
			Width:   width,
			Height:  height,
			MIME:    "image/png",
			Profile: profile,
		}
	}
	if report.Boundary != nil {
		var artifacts []api.HDRTypedArtifact
		for _, format := range report.Boundary.Formats {
			if format.Measured == 0 || len(format.Thumbnails) < 2 {
				continue
			}
			kind := api.HDRArtifactBoundaryDVSceneSheet
			if format.Format == api.HDRNativeHDR10Plus {
				kind = api.HDRArtifactBoundaryHDR10PlusSceneSheet
			}
			value := artifact(kind)
			value.Format = format.Format
			artifacts = append(artifacts, value)
		}
		return artifacts
	}
	if report.Audio != nil {
		var artifacts []api.HDRTypedArtifact
		for _, image := range report.Audio.Images {
			value := artifact(image.Kind)
			value.Width, value.Height = image.Width, image.Height
			if report.AudioAuthority != nil {
				value.AudioTrackID = report.AudioAuthority.TrackID
			}
			artifacts = append(artifacts, value)
		}
		if report.Audio.Stats != "" {
			value := artifact(api.HDRArtifactAudioStats)
			value.Width, value.Height, value.MIME = 0, 0, "text/plain"
			if report.AudioAuthority != nil {
				value.AudioTrackID = report.AudioAuthority.TrackID
			}
			artifacts = append(artifacts, value)
		}
		return artifacts
	}
	if report.Automatic != nil {
		artifacts := []api.HDRTypedArtifact{artifact(api.HDRArtifactPicturePlot)}
		if slices.ContainsFunc(report.Automatic.Frames, func(frame AutomaticFrame) bool { return frame.DVKnown }) {
			artifacts = append(artifacts, artifact(api.HDRArtifactDVOverlay), artifact(api.HDRArtifactDVDifference))
		}
		if slices.ContainsFunc(report.Automatic.Frames, func(frame AutomaticFrame) bool { return frame.HDR10PlusKnown }) {
			artifacts = append(artifacts, artifact(api.HDRArtifactHDR10PlusOverlay), artifact(api.HDRArtifactHDR10PlusDifference))
		}
		for _, scene := range report.Automatic.Scenes {
			kind := api.HDRArtifactDVSceneSheet
			if scene.Format == api.HDRNativeHDR10Plus {
				kind = api.HDRArtifactHDR10PlusSceneSheet
			}
			artifacts = append(artifacts, artifact(kind))
		}
		if len(report.Automatic.Areas) > 0 {
			artifacts = append(artifacts, artifact(api.HDRArtifactAutomaticArea))
		}
		if len(report.Automatic.Chromaticity[0]) > 0 || len(report.Automatic.Chromaticity[1]) > 0 {
			artifacts = append(artifacts, artifact(api.HDRArtifactChromaticity))
		}
		if len(report.Automatic.Reference) > 0 {
			artifacts = append(artifacts, artifact(api.HDRArtifactReferencePicturePlot))
			if report.Options.Comparison.Grade {
				artifacts = append(artifacts, artifact(api.HDRArtifactAutomaticGrade))
			}
			if report.Options.Comparison.Colour {
				artifacts = append(artifacts, artifact(api.HDRArtifactAutomaticColour))
			}
		}
		return artifacts
	}
	kind := map[api.HDRDecodedMode]api.HDRArtifactKind{
		api.HDRDecodedSceneSync:  api.HDRArtifactSceneContactSheet,
		api.HDRDecodedActiveArea: api.HDRArtifactActiveAreaContactSheet,
		api.HDRDecodedGrade:      api.HDRArtifactGradePlot,
		api.HDRDecodedColour:     api.HDRArtifactColourPlot,
	}[report.Result.Mode]
	if kind == "" {
		return nil
	}
	return []api.HDRTypedArtifact{artifact(kind)}
}

// RenderMeasurementArtifact publishes one retained output without source media.
func (s *Session) RenderMeasurementArtifact(ctx context.Context, report *MeasurementReport, kind api.HDRArtifactKind, title string, output io.Writer) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render automatic artifact: %w", err)
	}
	if !slices.ContainsFunc(MeasurementArtifacts(report), func(artifact api.HDRTypedArtifact) bool { return artifact.Kind == kind }) {
		return errors.New("measurement does not own this artifact")
	}
	if report.Audio != nil {
		data := []byte(report.Audio.Stats)
		if kind != api.HDRArtifactAudioStats {
			for _, image := range report.Audio.Images {
				if image.Kind == kind {
					data = image.Data
					break
				}
			}
		}
		if _, err := output.Write(data); err != nil {
			return fmt.Errorf("write retained audio artifact: %w", err)
		}
		return nil
	}
	if report.Boundary != nil {
		return renderBoundarySheet(ctx, report, kind, title, output)
	}
	if report.Automatic == nil {
		return s.RenderMeasurement(ctx, report, title, output)
	}
	width, height := kind.ImageDimensions()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	background := color.Color(color.White)
	if kind == api.HDRArtifactDVSceneSheet || kind == api.HDRArtifactHDR10PlusSceneSheet {
		background = measurementBackground
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	runes := []rune(title)
	title = string(runes[:min(len(runes), 140)])
	plot, err := newAutomaticPlot(canvas)
	if err != nil {
		return err
	}
	defer plot.face.Close()
	if background == color.White {
		plot.label(28, 28, title, color.Black)
	}
	//nolint:exhaustive // The owning automatic report has already restricted the selected artifact kind.
	switch kind {
	case api.HDRArtifactDVSceneSheet, api.HDRArtifactHDR10PlusSceneSheet:
		for _, scene := range report.Automatic.Scenes {
			if kind == api.HDRArtifactDVSceneSheet && scene.Format != api.HDRNativeDolbyVision ||
				kind == api.HDRArtifactHDR10PlusSceneSheet && scene.Format != api.HDRNativeHDR10Plus {
				continue
			}
			plot.sceneSheet(scene, title)
			if len(scene.Thumbnails) == 0 {
				measurementLabel(canvas, 28, 140, "No confident image pair retained; numeric scene observations remain available.", measurementText)
			}
		}
	case api.HDRArtifactAutomaticArea:
		plot.activeArea(report)
	case api.HDRArtifactChromaticity:
		plot.chromaticity(report)
	default:
		plot.curves(report, kind)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render automatic artifact: %w", err)
	}
	if err := png.Encode(&boundedWriter{output: output, remaining: 32 << 20}, canvas); err != nil {
		return fmt.Errorf("encode automatic artifact: %w", err)
	}
	return nil
}

func (p *automaticPlot) activeArea(report *MeasurementReport) {
	frames := report.Automatic.Frames
	total := max(1, automaticFrameTotal(report, false)-1)
	maximum := 1
	for _, frame := range frames {
		if frame.BorderKnown {
			for _, offset := range frame.Borders {
				maximum = max(maximum, offset)
			}
		}
	}
	for _, interval := range report.Automatic.Areas {
		if interval.Known {
			for _, offset := range interval.Expected {
				maximum = max(maximum, offset)
			}
		}
	}
	upper := 4 * automaticTickStep(float64(maximum)/4)
	rect := image.Rect(85, 140, p.canvas.Bounds().Max.X-30, 730)
	yAt := p.axes(
		rect,
		0,
		upper,
		[]float64{0, upper / 4, upper / 2, upper * 3 / 4, upper},
		float64(total),
		"source frames",
		"border inset (coded pixels)",
		false,
	)
	p.label(85, 65, "L5 intervals and observed picture borders; ambiguous dark frames excluded", color.Black)
	inks := []color.RGBA{automaticBlue, {215, 100, 0, 255}, automaticGreen, automaticViolet}
	for side, ink := range inks {
		for _, interval := range report.Automatic.Areas {
			if !interval.Known {
				continue
			}
			x0, x1 := rectX(rect.Min.X, rect.Max.X, interval.Start, total), rectX(rect.Min.X, rect.Max.X, interval.End-1, total)
			y := yAt(float64(interval.Expected[side]))
			measurementLine(p.canvas, x0, y, x1, y, ink)
		}
		for i, frame := range frames {
			if !frame.BorderKnown {
				continue
			}
			x := rectX(rect.Min.X, rect.Max.X, automaticFrameNumber(report, i, false), total)
			y := yAt(float64(frame.Borders[side]))
			measurementLine(p.canvas, x-2, y, x+2, y, ink)
			measurementLine(p.canvas, x, y-2, x, y+2, ink)
		}
	}
	p.label(85, 820, "Blue left | Orange right | Green top | Violet bottom | Lines: L5; crosses: picture", color.Black)
	if report.Automatic.Sampling != nil {
		p.label(85, 850, "Sampled borders; intervals without measured pictures remain unclear", color.Gray{Y: 90})
	}
}

func picturePQ(nits float64) float64 { value, _ := dovi.NitsToPQ(nits); return value * 1023 }

func automaticCurveValues(report *MeasurementReport, kind api.HDRArtifactKind, index int) ([]float64, bool) {
	if kind == api.HDRArtifactReferencePicturePlot {
		if index >= len(report.Automatic.Reference) {
			return nil, false
		}
		frame := report.Automatic.Reference[index]
		return []float64{picturePQ(frame.MaxRGBNits), picturePQ(frame.FALLNits)}, true
	}
	frame := report.Automatic.Frames[index]
	measured := [2]float64{picturePQ(frame.MaxRGBNits), picturePQ(frame.FALLNits)}
	//nolint:exhaustive // Only the curve subset of automatic artifact kinds has numeric series.
	switch kind {
	case api.HDRArtifactPicturePlot:
		return measured[:], true
	case api.HDRArtifactDVOverlay, api.HDRArtifactDVDifference:
		if !frame.DVKnown {
			return nil, false
		}
		if kind == api.HDRArtifactDVOverlay {
			return []float64{measured[0], measured[1], frame.DVPQ[0], frame.DVPQ[1]}, true
		}
		return []float64{frame.DVPQ[0] - measured[0], frame.DVPQ[1] - measured[1]}, true
	case api.HDRArtifactHDR10PlusOverlay, api.HDRArtifactHDR10PlusDifference:
		if !frame.HDR10PlusKnown {
			return nil, false
		}
		if kind == api.HDRArtifactHDR10PlusOverlay {
			return []float64{measured[0], measured[1], frame.HDR10PlusPQ[0], frame.HDR10PlusPQ[1]}, true
		}
		return []float64{frame.HDR10PlusPQ[0] - measured[0], frame.HDR10PlusPQ[1] - measured[1]}, true
	case api.HDRArtifactAutomaticGrade, api.HDRArtifactAutomaticColour:
		comparison := report.Options.Comparison
		if comparison == nil || comparison.FrameOffset == nil {
			return nil, false
		}
		otherNumber := automaticFrameNumber(report, index, false) + *comparison.FrameOffset
		other, exists := automaticFrameAt(report, otherNumber, true)
		if !exists {
			return nil, false
		}
		if kind == api.HDRArtifactAutomaticGrade {
			return []float64{measured[0] - picturePQ(other.MaxRGBNits), measured[1] - picturePQ(other.FALLNits)}, true
		}
		if frame.ColourPixels == 0 || other.ColourPixels == 0 {
			return nil, false
		}
		return []float64{
			1023 * (frame.MeanRGBPQ[0] - other.MeanRGBPQ[0]),
			1023 * (frame.MeanRGBPQ[1] - other.MeanRGBPQ[1]),
			1023 * (frame.MeanRGBPQ[2] - other.MeanRGBPQ[2]),
		}, true
	}
	return nil, false
}
