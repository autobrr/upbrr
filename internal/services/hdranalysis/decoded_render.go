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
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	measurementBackground = color.RGBA{16, 24, 38, 255}
	measurementText       = color.RGBA{223, 232, 244, 255}
	measurementGrid       = color.RGBA{60, 76, 96, 255}
	measurementA          = color.RGBA{59, 210, 238, 255}
	measurementB          = color.RGBA{255, 166, 73, 255}
)

func measurementLabel(canvas *image.RGBA, x, y int, label string, ink color.Color) {
	drawer := font.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(ink),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	drawer.DrawString(label)
}

func measurementLine(canvas *image.RGBA, x0, y0, x1, y1 int, ink color.Color) {
	dx, dy := int(math.Abs(float64(x1-x0))), -int(math.Abs(float64(y1-y0)))
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		canvas.Set(x0, y0, ink)
		if x0 == x1 && y0 == y1 {
			return
		}
		doubled := 2 * err
		if doubled >= dy {
			err, x0 = err+dy, x0+sx
		}
		if doubled <= dx {
			err, y0 = err+dx, y0+sy
		}
	}
}

// RenderMeasurement draws only retained numeric evidence and reduced thumbnails.
// It never reads source media and can therefore run after the source is removed.
func (s *Session) RenderMeasurement(ctx context.Context, report *MeasurementReport, title string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render decoded measurement: %w", err)
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if report.Result.Measured == 0 || report.Result.State != api.HDRDecodedComplete && report.Result.State != api.HDRDecodedInconclusive {
		return errors.New("measurement has no renderable evidence")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(measurementBackground), image.Point{}, draw.Src)
	titleRunes := []rune(title)
	measurementLabel(canvas, 28, 30, string(titleRunes[:min(len(titleRunes), 140)]), measurementText)
	measurementLabel(
		canvas,
		28,
		54,
		fmt.Sprintf("%s | %s | selected %d | measured %d", report.Result.Profile, report.Result.State, report.Result.Selected, report.Result.Measured),
		measurementText,
	)
	measurementLabel(canvas, 28, 78, "Review evidence only. Findings do not certify synchronization, identical grades, or hybrid safety.", measurementText)
	switch report.Result.Mode {
	case api.HDRDecodedAutomatic, api.HDRDecodedAudio, api.HDRDecodedBoundarySync:
		return errors.New("automatic measurements require a typed artifact selection")
	case api.HDRDecodedSceneSync, api.HDRDecodedActiveArea:
		renderMeasurementThumbnails(canvas, report)
	case api.HDRDecodedGrade:
		renderMeasurementGrade(canvas, report)
	case api.HDRDecodedColour:
		renderMeasurementColour(canvas, report)
	}
	measurementLabel(
		canvas,
		28,
		877,
		"PQ BT.2020 base-layer analysis | Full numeric observations and exact timing are retained in the report.",
		measurementText,
	)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render decoded measurement: %w", err)
	}
	if err := png.Encode(&boundedWriter{output: output, remaining: 32 << 20}, canvas); err != nil {
		return fmt.Errorf("encode decoded measurement: %w", err)
	}
	return nil
}

func renderMeasurementThumbnails(canvas *image.RGBA, report *MeasurementReport) {
	for i, thumbnail := range report.Thumbnails {
		x, y := 28+(i%6)*194, 118+(i/6)*178
		for row := range thumbnail.Height {
			for col := range thumbnail.Width {
				v := thumbnail.Luma[row*thumbnail.Width+col]
				canvas.SetRGBA(x+col, y+row, color.RGBA{v, v, v, 255})
			}
		}
		measurementLabel(canvas, x, y+128, fmt.Sprintf("frame %d", thumbnail.Index), measurementText)
		if report.Result.Mode == api.HDRDecodedSceneSync {
			for _, scene := range report.Scene {
				if scene.Candidate == thumbnail.Candidate {
					measurementLabel(canvas, x, y+145, fmt.Sprintf("candidate %d | offset %+d", scene.Candidate, scene.BestOffset), measurementText)
					measurementLabel(canvas, x, y+161, scene.Confidence, measurementText)
				}
			}
		}
		if report.Result.Mode == api.HDRDecodedActiveArea {
			for _, area := range report.Area {
				if thumbnail.Index < area.Start || thumbnail.Index >= area.End || !area.Known || report.Raster[0] == 0 || report.Raster[1] == 0 {
					continue
				}
				for j, offsets := range [][4]int{area.Expected, area.Observed} {
					ink := measurementA
					if j == 1 {
						ink = measurementB
					}
					left := x + min(191, offsets[0]*192/report.Raster[0])
					right := x + max(0, 191-offsets[1]*192/report.Raster[0])
					top := y + min(107, offsets[2]*108/report.Raster[1])
					bottom := y + max(0, 107-offsets[3]*108/report.Raster[1])
					measurementLine(canvas, left, top, right, top, ink)
					measurementLine(canvas, right, top, right, bottom, ink)
					measurementLine(canvas, right, bottom, left, bottom, ink)
					measurementLine(canvas, left, bottom, left, top, ink)
				}
				measurementLabel(canvas, x, y+146, area.Confidence, measurementText)
			}
		}
	}
	if report.Result.Mode == api.HDRDecodedActiveArea {
		measurementLabel(canvas, 28, 835, "Cyan: metadata L5 | Orange: observed border | Native coordinates; thumbnail luma is a scene cue.", measurementText)
	} else {
		measurementLabel(
			canvas,
			28,
			835,
			"Adjacent pairs show the strongest local image change. Refresh/L1 changes are candidates, not cut truth.",
			measurementText,
		)
	}
}

func renderMeasurementGrade(canvas *image.RGBA, report *MeasurementReport) {
	if len(report.Grade) == 0 {
		return
	}
	left, right := report.Grade[0].IndexA, report.Grade[len(report.Grade)-1].IndexA
	xAt := func(index int) int { return 85 + (index-left)*1060/max(1, right-left) }
	for _, bounds := range [][2]int{{125, 430}, {520, 795}} {
		measurementLine(canvas, 85, bounds[0], 85, bounds[1], measurementGrid)
		measurementLine(canvas, 85, bounds[1], 1145, bounds[1], measurementGrid)
	}
	measurementLabel(canvas, 85, 109, "Absolute light: log10(1 + nits). Solid bright: MaxRGB; darker: mean luminance Y.", measurementText)
	for _, nits := range []float64{0, 1, 10, 100, 1000, 10000} {
		y := 430 - int(305*math.Log10(nits+1)/math.Log10(10001))
		measurementLine(canvas, 85, y, 1145, y, measurementGrid)
		measurementLabel(canvas, 22, y+4, fmt.Sprintf("%.0f", nits), measurementText)
	}
	inks := []color.RGBA{measurementA, measurementB, {30, 116, 150, 255}, {157, 81, 42, 255}}
	for curve, ink := range inks {
		lastX, lastY := 0, 0
		for i, grade := range report.Grade {
			value := []float64{grade.A.MaxRGBNits, grade.B.MaxRGBNits, grade.A.MeanYNits, grade.B.MeanYNits}[curve]
			x, y := xAt(grade.IndexA), 430-int(305*math.Log10(value+1)/math.Log10(10001))
			if i > 0 {
				measurementLine(canvas, lastX, lastY, x, y, ink)
			}
			canvas.Set(x, y, ink)
			lastX, lastY = x, y
		}
	}
	measurementLabel(canvas, 85, 466, "Cyan: A | Orange: B | Same explicit crop, exact 2x2 linear averaging; no tone mapping.", measurementText)
	measurementLabel(canvas, 85, 505, "Signed mean channel PQ difference (B - A), normalized PQ units; joint luminance floor 1 nit.", measurementText)
	span := 0.001
	for _, grade := range report.Grade {
		for _, value := range grade.Difference.SignedMeanPQ {
			span = max(span, math.Abs(value))
		}
	}
	measurementLine(canvas, 85, 658, 1145, 658, measurementGrid)
	measurementLabel(canvas, 20, 530, fmt.Sprintf("+%.3f", span), measurementText)
	measurementLabel(canvas, 20, 794, fmt.Sprintf("-%.3f", span), measurementText)
	for channel, ink := range []color.RGBA{{255, 115, 115, 255}, {130, 220, 130, 255}, {130, 160, 255, 255}} {
		lastX, lastY := 0, 0
		for i, grade := range report.Grade {
			x, y := xAt(grade.IndexA), 658-int(137*grade.Difference.SignedMeanPQ[channel]/span)
			if i > 0 {
				measurementLine(canvas, lastX, lastY, x, y, ink)
			}
			lastX, lastY = x, y
		}
	}
	measurementLabel(canvas, 85, 822, fmt.Sprintf("A presentation frames %d ... %d | Red / Green / Blue channel means", left, right), measurementText)
}

func renderMeasurementColour(canvas *image.RGBA, report *MeasurementReport) {
	measurementLabel(canvas, 85, 112, "CIE 1931 xy | BT.2020/D65 triangle | Linear Y >= 1 nit | Bounded deterministic pixel samples", measurementText)
	for side, points := range report.Chromaticity {
		ink, origin := measurementA, 60+side*590
		if side == 1 {
			ink = measurementB
		}
		xAt := func(x float64) int { return origin + int(x*600) }
		yAt := func(y float64) int { return 770 - int(y*720) }
		measurementLine(canvas, origin, 770, origin+500, 770, measurementGrid)
		measurementLine(canvas, origin, 770, origin, 160, measurementGrid)
		for _, pair := range [][2][2]float64{
			{{0.708, 0.292}, {0.170, 0.797}}, {{0.170, 0.797}, {0.131, 0.046}}, {{0.131, 0.046}, {0.708, 0.292}},
		} {
			measurementLine(canvas, xAt(pair[0][0]), yAt(pair[0][1]), xAt(pair[1][0]), yAt(pair[1][1]), measurementGrid)
		}
		for _, point := range points {
			x, y := xAt(point.X), yAt(point.Y)
			draw.Draw(canvas, image.Rect(x-1, y-1, x+2, y+2), image.NewUniform(ink), image.Point{}, draw.Src)
		}
		wx, wy := xAt(0.3127), yAt(0.3290)
		measurementLine(canvas, wx-5, wy, wx+5, wy, measurementText)
		measurementLine(canvas, wx, wy-5, wx, wy+5, measurementText)
		measurementLabel(canvas, origin, 800, fmt.Sprintf("Source %c | %d sampled pixels | + D65", 'A'+side, len(points)), ink)
		measurementLabel(canvas, origin, 824, "x: 0 ... 0.83 | y: 0 ... 0.85", measurementText)
	}
}
