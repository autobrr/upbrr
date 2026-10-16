// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"

	"github.com/autobrr/upbrr/pkg/api"
)

func renderBoundarySheet(ctx context.Context, report *MeasurementReport, kind api.HDRArtifactKind, title string, output io.Writer) error {
	format := api.HDRNativeDolbyVision
	if kind == api.HDRArtifactBoundaryHDR10PlusSceneSheet {
		format = api.HDRNativeHDR10Plus
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1800, 900))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(measurementBackground), image.Point{}, draw.Src)
	plot, err := newAutomaticPlot(canvas)
	if err != nil {
		return err
	}
	defer plot.face.Close()
	plot.label(20, 28, title+" | "+string(format)+" boundary synchronization", measurementText)
	for _, evidence := range report.Boundary.Formats {
		if evidence.Format != format {
			continue
		}
		plot.label(20, 52, fmt.Sprintf(
			"Coverage: %s | Measured %d/%d candidates | %d/12 image pairs | Missing numeric/visual strata: %d/%d",
			evidence.Coverage,
			evidence.Measured,
			evidence.Selected,
			len(evidence.Thumbnails)/2,
			evidence.MissingNumericStrata,
			evidence.MissingVisualStrata,
		), measurementText)
		plot.label(
			20,
			76,
			"SDR colour previews (grayscale if unavailable). Pairs stay before/at the metadata boundary; offsets reported separately.",
			measurementText,
		)
		for pair := 0; pair < len(evidence.Thumbnails)/2; pair++ {
			before, after := evidence.Thumbnails[2*pair], evidence.Thumbnails[2*pair+1]
			x, y := 20+(pair%3)*594, 103+(pair/3)*194
			label := fmt.Sprintf("Boundary %d", before.Candidate)
			for _, observation := range evidence.Observations {
				if observation.Candidate == before.Candidate {
					label = fmt.Sprintf("Boundary %d | offset %+d | %s", before.Candidate, observation.BestOffset, observation.Confidence)
					break
				}
			}
			plot.label(x, y, label, measurementText)
			for side, thumb := range []MeasurementThumbnail{before, after} {
				for row := range 151 {
					for col := range 268 {
						pixel := (row*thumb.Height/151)*thumb.Width + col*thumb.Width/268
						value := thumb.Luma[pixel]
						ink := color.RGBA{value, value, value, 255}
						if len(thumb.RGB) != 0 {
							ink = color.RGBA{thumb.RGB[3*pixel], thumb.RGB[3*pixel+1], thumb.RGB[3*pixel+2], 255}
						}
						canvas.SetRGBA(x+side*286+col, y+10+row, ink)
					}
				}
				plot.label(x+side*286, y+180, fmt.Sprintf("Frame %d", thumb.Index), measurementText)
			}
		}
		if len(evidence.Thumbnails) == 0 {
			plot.label(20, 130, "No proved visual pair is available. Numeric measurements remain in the retained report.", measurementText)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render boundary sheet: %w", err)
	}
	if err := png.Encode(&boundedWriter{output: output, remaining: 32 << 20}, canvas); err != nil {
		return fmt.Errorf("encode boundary sheet: %w", err)
	}
	return nil
}
