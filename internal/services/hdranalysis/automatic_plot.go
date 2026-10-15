// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"

	dovi "github.com/Audionut/go-dovi-extract"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/autobrr/upbrr/pkg/api"
)

// These layouts follow HDRplot and gradecheck: nit-labelled brightness curves,
// separate FALL/MaxRGB differences, and co-located xy clouds per timestamp.
// Existing native metadata plots remain owned by their extraction libraries.
type automaticPlot struct {
	canvas *image.RGBA
	face   font.Face
}

var (
	automaticBlue   = color.RGBA{65, 105, 225, 255}
	automaticViolet = color.RGBA{138, 43, 226, 255}
	automaticGreen  = color.RGBA{46, 139, 87, 255}
	automaticGrid   = color.RGBA{225, 225, 225, 255}
)

func newAutomaticPlot(canvas *image.RGBA) (*automaticPlot, error) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("parse plot font: %w", err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 16, DPI: 72})
	if err != nil {
		return nil, fmt.Errorf("create plot font: %w", err)
	}
	return &automaticPlot{canvas: canvas, face: face}, nil
}

func (p *automaticPlot) label(x, y int, text string, ink color.Color) {
	for len(text) > 0 && font.MeasureString(p.face, text).Ceil() > p.canvas.Bounds().Max.X-x-12 {
		runes := []rune(text)
		text = string(runes[:len(runes)-1])
	}
	drawer := font.Drawer{
		Dst:  p.canvas,
		Src:  image.NewUniform(ink),
		Face: p.face,
		Dot:  fixed.P(x, y),
	}
	drawer.DrawString(text)
}

func automaticTickStep(minimum float64) float64 {
	scale := math.Pow(10, math.Floor(math.Log10(minimum)))
	for _, multiple := range []float64{1, 2, 5, 10} {
		if step := multiple * scale; step >= minimum {
			return step
		}
	}
	return 10 * scale
}

func (p *automaticPlot) axes(
	rect image.Rectangle,
	lower, upper float64,
	ticks []float64,
	xMax float64,
	xLabel, yLabel string,
	logarithmic bool,
) func(float64) int {
	yAt := func(value float64) int {
		value = max(lower, min(upper, value))
		fraction := (value - lower) / (upper - lower)
		if logarithmic {
			fraction = math.Log(value/lower) / math.Log(upper/lower)
		}
		return rect.Max.Y - int(float64(rect.Dy())*fraction)
	}
	for _, tick := range ticks {
		y := yAt(tick)
		measurementLine(p.canvas, rect.Min.X, y, rect.Max.X, y, automaticGrid)
		label := fmt.Sprintf("%.4g", tick)
		p.label(rect.Min.X-font.MeasureString(p.face, label).Ceil()-12, y+5, label, color.Black)
	}
	step := automaticTickStep(xMax / 10)
	if xLabel == "source frames" {
		step = max(1, step)
	}
	for i := 0; float64(i)*step <= xMax; i++ {
		value := float64(i) * step
		x := rect.Min.X + int(float64(rect.Dx())*value/xMax)
		measurementLine(p.canvas, x, rect.Min.Y, x, rect.Max.Y, automaticGrid)
		label := fmt.Sprintf("%.6g", value)
		labelX := max(5, min(x-font.MeasureString(p.face, label).Ceil()/2, p.canvas.Bounds().Max.X-font.MeasureString(p.face, label).Ceil()-20))
		p.label(labelX, rect.Max.Y+22, label, color.Black)
	}
	for _, edge := range [][2]image.Point{{rect.Min, {rect.Max.X, rect.Min.Y}}, {{rect.Max.X, rect.Min.Y}, rect.Max},
		{rect.Max, {rect.Min.X, rect.Max.Y}}, {{rect.Min.X, rect.Max.Y}, rect.Min}} {
		measurementLine(p.canvas, edge[0].X, edge[0].Y, edge[1].X, edge[1].Y, color.Black)
	}
	p.label(rect.Min.X, rect.Min.Y-14, yLabel, color.Black)
	p.label(rect.Min.X+rect.Dx()/2, rect.Max.Y+45, xLabel, color.Black)
	return yAt
}

func plotPercentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	slices.Sort(values)
	position := fraction * float64(len(values)-1)
	lo := int(position)
	return values[lo] + (values[min(lo+1, len(values)-1)]-values[lo])*(position-float64(lo))
}

func (p *automaticPlot) curves(report *MeasurementReport, kind api.HDRArtifactKind) {
	if kind == api.HDRArtifactAutomaticColour {
		p.colourBias(report)
		return
	}
	if kind == api.HDRArtifactDVDifference || kind == api.HDRArtifactHDR10PlusDifference || kind == api.HDRArtifactAutomaticGrade {
		p.differences(report, kind)
		return
	}
	frames := report.Automatic.Frames
	if kind == api.HDRArtifactReferencePicturePlot {
		frames = report.Automatic.Reference
	}
	count := len(frames)
	reference := kind == api.HDRArtifactReferencePicturePlot
	xMax := max(1, automaticFrameTotal(report, reference)-1)
	xAt := func(i int) int {
		return rectX(85, p.canvas.Bounds().Max.X-25, automaticFrameNumber(report, i, reference), xMax)
	}
	rect := image.Rect(85, 110, p.canvas.Bounds().Max.X-25, 610)
	yAt := p.axes(rect, 0.1, 10000, []float64{0.1, 0.3, 1, 3, 10, 30, 100, 300, 1000, 3000, 10000},
		float64(xMax), "source frames", "nits (cd/m2)", true)
	p.label(85, 65, "Measured picture: BT.2020 / PQ; active area, half-resolution linear-light box average", color.Black)
	if report.Automatic.Profile5 && kind != api.HDRArtifactReferencePicturePlot {
		p.label(85, 87, "Dolby Vision Profile 5 reshaped through its RPU; clipping, no peak detection", color.Black)
	}
	inks := []color.RGBA{automaticBlue, automaticViolet, {255, 0, 0, 255}, {255, 140, 0, 255}}
	previous, valid := make([]image.Point, 4), make([]bool, 4)
	maximums, falls := make([]float64, 0, count), make([]float64, 0, count)
	var peak, avgMax, avgFall float64
	for i, frame := range frames {
		x := xAt(i)
		maxY, fallY := yAt(frame.MaxRGBNits), yAt(frame.FALLNits)
		previousX, previousMaxY, previousFallY := x, maxY, fallY
		if i > 0 {
			previousX = xAt(i - 1)
			previousMaxY, previousFallY = yAt(frames[i-1].MaxRGBNits), yAt(frames[i-1].FALLNits)
		}
		for column := previousX; column <= x; column++ {
			upperY := previousMaxY + (maxY-previousMaxY)*(column-previousX)/max(1, x-previousX)
			lowerY := previousFallY + (fallY-previousFallY)*(column-previousX)/max(1, x-previousX)
			measurementLine(p.canvas, column, upperY, column, lowerY, color.RGBA{174, 190, 243, 255})
			measurementLine(p.canvas, column, lowerY, column, rect.Max.Y, color.RGBA{208, 170, 244, 255})
		}
	}
	for i, frame := range frames {
		x := xAt(i)
		values, available := automaticCurveValues(report, kind, i)
		if !available {
			values = []float64{picturePQ(frame.MaxRGBNits), picturePQ(frame.FALLNits)}
		}
		for series := range inks {
			if series >= len(values) {
				valid[series] = false
				continue
			}
			nits, _ := dovi.PQToNits(values[series] / 1023)
			point := image.Pt(x, yAt(nits))
			if valid[series] {
				measurementLine(p.canvas, previous[series].X, previous[series].Y, point.X, point.Y, inks[series])
			}
			previous[series], valid[series] = point, true
		}
		maximums, falls = append(maximums, picturePQ(frame.MaxRGBNits)), append(falls, picturePQ(frame.FALLNits))
		peak, avgMax, avgFall = max(peak, frame.MaxRGBNits), avgMax+frame.MaxRGBNits, avgFall+frame.FALLNits
	}
	maxCLL, _ := dovi.PQToNits(plotPercentile(maximums, .995) / 1023)
	maxFALL, _ := dovi.PQToNits(plotPercentile(falls, .9975) / 1023)
	legend := image.Rect(95, 520, 870, 600)
	draw.Draw(p.canvas, legend, image.White, image.Point{}, draw.Src)
	p.label(105, 540, fmt.Sprintf("CLL: maxCLL (99.5%%) %.2f nits; avgCLL %.2f nits", maxCLL, avgMax/float64(count)), automaticBlue)
	p.label(105, 562, fmt.Sprintf("FALL: maxFALL (99.75%%) %.2f nits; avgFALL %.2f nits", maxFALL, avgFall/float64(count)), automaticViolet)
	if kind == api.HDRArtifactDVOverlay || kind == api.HDRArtifactHDR10PlusOverlay {
		name := "RPU L1"
		if kind == api.HDRArtifactHDR10PlusOverlay {
			name = "HDR10+ MaxSCL / average RGB"
		}
		p.label(105, 584, name+": red maximum / dark orange average (metadata statistics)", color.Black)
	}
	coverage := "Every presentation frame; true picture peak"
	if report.Automatic.Sampling != nil {
		coverage = fmt.Sprintf("Sampled every %d source frames; highest sampled peak", report.Automatic.Sampling.Step)
		p.label(85, 665, "Sample percentiles and averages; connecting lines guide the eye. Brief peaks between samples can be missed.", color.Gray{Y: 90})
	}
	p.label(85, 695, fmt.Sprintf("%s %.2f nits. Numeric evidence and processing details retained in the report.", coverage, peak), color.Gray{Y: 90})
}

func rectX(left, right, index, maximum int) int { return left + (right-left)*index/max(1, maximum) }

func (p *automaticPlot) differences(report *MeasurementReport, kind api.HDRArtifactKind) {
	count := len(report.Automatic.Frames)
	xMax := max(1, automaticFrameTotal(report, false)-1)
	step := 1
	if report.Automatic.Sampling != nil {
		step = report.Automatic.Sampling.Step
	}
	definition := "Primary picture minus HDR10 reference"
	//nolint:exhaustive // This renderer accepts only the three difference artifact kinds.
	switch kind {
	case api.HDRArtifactDVDifference:
		definition = "RPU L1 minus measured picture; different statistics"
	case api.HDRArtifactHDR10PlusDifference:
		definition = "HDR10+ metadata minus measured picture; different statistics"
	}
	for panel, series := range []int{1, 0} {
		values, indices := make([]float64, 0, count), make([]int, 0, count)
		span := 30.0
		name, ink := "FALL / average", automaticViolet
		if series == 0 {
			span, name, ink = 60, "MaxRGB / maximum", automaticBlue
		}
		for i, frame := range report.Automatic.Frames {
			value, available := automaticCurveValues(report, kind, i)
			if available && frame.FALLNits > .05 {
				values, indices = append(values, value[series]), append(indices, automaticFrameNumber(report, i, false))
				span = max(span, math.Abs(value[series]))
			}
		}
		span = 2 * automaticTickStep(span/2)
		rect := image.Rect(85, 110+panel*350, 1775, 355+panel*350)
		yAt := p.axes(rect, -span, span, []float64{-span, -span / 2, 0, span / 2, span}, float64(xMax), "source frames", "Delta 10-bit PQ codes", false)
		summary := fmt.Sprintf("%s: %s; no eligible measured samples", name, definition)
		if len(values) > 0 {
			summary = fmt.Sprintf("%s: %s; median %+.2f, p5/p95 %+.2f / %+.2f", name, definition,
				plotPercentile(slices.Clone(values), .5), plotPercentile(slices.Clone(values), .05), plotPercentile(slices.Clone(values), .95))
		}
		p.label(85, rect.Min.Y-42, summary+fmt.Sprintf(" | n=%d", len(values)), color.Black)
		measurementLine(p.canvas, rect.Min.X, yAt(0), rect.Max.X, yAt(0), color.Gray{Y: 100})
		var previous image.Point
		for i, value := range values {
			point := image.Pt(rectX(rect.Min.X, rect.Max.X, indices[i], xMax), yAt(value))
			if i > 0 && indices[i] == indices[i-1]+step {
				measurementLine(p.canvas, previous.X, previous.Y, point.X, point.Y, ink)
			}
			p.canvas.Set(point.X, point.Y, ink)
			previous = point
		}
	}
	caption := "Black frames excluded (picture FALL <= 0.05 nits). Axis expands to retain every measured difference."
	if report.Automatic.Sampling != nil {
		caption = fmt.Sprintf(
			"Sampled every %d source frames; brief mismatches can be missed. Black frames excluded (FALL <= 0.05 nits); lines connect measured samples.",
			step,
		)
	}
	p.label(85, 785, caption, color.Gray{Y: 90})
}

func automaticSeconds(frame AutomaticFrame) float64 {
	time, err := automaticTime(frame.PTS)
	if err != nil {
		return 0
	}
	return float64(time.Value) / float64(time.Timescale)
}

func (p *automaticPlot) colourBias(report *MeasurementReport) {
	var values [3][]float64
	var times []float64
	last := -30.0
	span := 1.0
	for i, frame := range report.Automatic.Frames {
		seconds := automaticSeconds(frame) - automaticSeconds(report.Automatic.Frames[0])
		channels, available := automaticCurveValues(report, api.HDRArtifactAutomaticColour, i)
		if !available || seconds-last < 30 {
			continue
		}
		last = seconds
		times = append(times, seconds/60)
		for channel, value := range channels {
			values[channel] = append(values[channel], value)
			span = max(span, math.Abs(value))
		}
	}
	rect := image.Rect(85, 155, 1975, 575)
	duration := (automaticSeconds(report.Automatic.Frames[len(report.Automatic.Frames)-1]) - automaticSeconds(report.Automatic.Frames[0])) / 60
	span = 2 * automaticTickStep(span/2)
	yAt := p.axes(
		rect,
		-span*1.1,
		span*1.1,
		[]float64{-span, -span / 2, 0, span / 2, span},
		max(1, duration),
		"minutes",
		"Mean channel difference, 10-bit PQ codes",
		false,
	)
	label := "Primary minus HDR10 reference, R / G / B; one frame every 30 seconds"
	if report.Automatic.Sampling != nil {
		label = fmt.Sprintf("Primary minus HDR10 reference, R / G / B; sampled every %d source frames", report.Automatic.Sampling.Step*30)
	}
	p.label(85, 62, label, color.Black)
	p.label(85, 85, "Identical common crop; each source's pixels above 1 nit. Independently lit means; see numeric coverage.", color.Black)
	measurementLine(p.canvas, rect.Min.X, yAt(0), rect.Max.X, yAt(0), color.Black)
	if len(times) == 0 {
		p.label(85, 120, "No eligible paired colour samples were measured; no channel difference is established.", color.Black)
		return
	}
	for channel, ink := range []color.RGBA{{220, 20, 60, 255}, {0, 128, 0, 255}, automaticBlue} {
		p.label(100+channel*610, 120, fmt.Sprintf("%c: median %+.2f; p5/p95 %+.2f / %+.2f", "RGB"[channel], plotPercentile(slices.Clone(values[channel]), .5),
			plotPercentile(slices.Clone(values[channel]), .05), plotPercentile(slices.Clone(values[channel]), .95)), ink)
		var previous image.Point
		for i, value := range values[channel] {
			point := image.Pt(rect.Min.X+int(float64(rect.Dx())*times[i]/max(1, duration)), yAt(value))
			if i > 0 {
				measurementLine(p.canvas, previous.X, previous.Y, point.X, point.Y, ink)
			}
			draw.Draw(p.canvas, image.Rect(point.X-2, point.Y-2, point.X+3, point.Y+3), image.NewUniform(ink), image.Point{}, draw.Src)
			previous = point
		}
	}
}

func (p *automaticPlot) sceneSheet(scene AutomaticScene, title string) {
	p.label(20, 30, title+" | "+string(scene.Format)+" / picture cut alignment", measurementText)
	p.label(20, 55, fmt.Sprintf("Retained %d before/at pairs from %d scored candidates; empty positions have no retained pair.",
		len(scene.Thumbnails)/2, len(scene.Observations)), measurementText)
	for i := 0; i+1 < len(scene.Thumbnails); i += 2 {
		before, after := scene.Thumbnails[i], scene.Thumbnails[i+1]
		x, y := 16+(i/2%3)*596, 85+(i/2/3)*205
		p.label(x, y, fmt.Sprintf("Metadata cut %d", before.Candidate), measurementText)
		for _, observation := range scene.Observations {
			if observation.Candidate == before.Candidate {
				p.label(x+275, y, fmt.Sprintf("Picture cut offset %+d", observation.BestOffset), measurementText)
			}
		}
		for side, thumbnail := range []MeasurementThumbnail{before, after} {
			thumb := image.NewRGBA(image.Rect(0, 0, thumbnail.Width, thumbnail.Height))
			for pixel, luma := range thumbnail.Luma {
				ink := color.RGBA{luma, luma, luma, 255}
				if len(thumbnail.RGB) != 0 {
					ink = color.RGBA{thumbnail.RGB[3*pixel], thumbnail.RGB[3*pixel+1], thumbnail.RGB[3*pixel+2], 255}
				}
				thumb.SetRGBA(pixel%thumbnail.Width, pixel/thumbnail.Width, ink)
			}
			// Preserve the coded-raster thumbnail aspect in each before/at pair.
			for row := range 162 {
				for column := range 288 {
					p.canvas.Set(x+side*296+column, y+12+row, thumb.At(column*thumbnail.Width/288, row*thumbnail.Height/162))
				}
			}
			p.label(x+side*296, y+193, fmt.Sprintf("Frame %d", thumbnail.Index), measurementText)
		}
	}
}

// CIE 1931 spectral-locus coordinates, at 10 nm intervals, as used by
// gradecheck's colour diagram. The closing edge is the line of purples.
var automaticLocus = [][2]float64{
	{.1741, .005},
	{.1733, .0048},
	{.1714, .0051},
	{.1689, .0069},
	{.1644, .0109},
	{.1566, .0177},
	{.144, .0297},
	{.1241, .0578},
	{.0913, .1327},
	{.0454, .295},
	{.0082, .5384},
	{.0139, .7502},
	{.0743, .8338},
	{.1547, .8059},
	{.2296, .7543},
	{
		.3016,
		.6923,
	},
	{.3731, .6245},
	{.4441, .5547},
	{.5125, .4866},
	{.5752, .4242},
	{.627, .3725},
	{.6658, .334},
	{.6915, .3083},
	{.7079, .292},
	{.719, .2809},
	{.7347, .2653},
}

func (p *automaticPlot) chromaticity(report *MeasurementReport) {
	indices := make([]int, 0, 16)
	for _, point := range report.Automatic.Chromaticity[0] {
		if len(indices) == 0 || indices[len(indices)-1] != point.Index {
			indices = append(indices, point.Index)
		}
	}
	if len(indices) == 0 {
		offset := 0
		if report.Options.Comparison != nil && report.Options.Comparison.FrameOffset != nil {
			offset = *report.Options.Comparison.FrameOffset
		}
		for _, point := range report.Automatic.Chromaticity[1] {
			index := point.Index - offset
			if _, exists := automaticFrameAt(report, index, false); exists && (len(indices) == 0 || indices[len(indices)-1] != index) {
				indices = append(indices, index)
			}
		}
	}
	indices = selectAutomaticChromaticity(report, indices)
	legend := "CIE 1931 xy | Active pixels above 0.5 nit | Blue: primary | Bounded retained pixel clouds"
	if len(report.Automatic.Reference) > 0 {
		legend = "CIE 1931 xy | Active pixels above 0.5 nit | Blue: primary; green: HDR10 reference | Bounded retained pixel clouds"
	}
	p.label(
		30,
		58,
		legend,
		color.Black,
	)
	for panel, index := range indices {
		left := 55 + panel*455
		rect := image.Rect(left, 130, left+390, 572)
		yAt := p.axes(rect, 0, .85, []float64{0, .2, .4, .6, .8}, .75, "x", "y", false)
		xAt := func(value float64) int { return left + int(max(0, min(.75, value))/.75*390) }
		for i, xy := range automaticLocus {
			previous := automaticLocus[(i+len(automaticLocus)-1)%len(automaticLocus)]
			measurementLine(p.canvas, xAt(previous[0]), yAt(previous[1]), xAt(xy[0]), yAt(xy[1]), color.Black)
		}
		triangle := [][2]float64{{.708, .292}, {.170, .797}, {.131, .046}, {.708, .292}}
		for i := 1; i < len(triangle); i++ {
			measurementLine(p.canvas, xAt(triangle[i-1][0]), yAt(triangle[i-1][1]), xAt(triangle[i][0]), yAt(triangle[i][1]), color.Gray{Y: 155})
		}
		frame, _ := automaticFrameAt(report, index, false)
		seconds := automaticSeconds(frame) - automaticSeconds(report.Automatic.Frames[0])
		p.label(left+125, 87, fmt.Sprintf("%d:%02d | frame %d", int(seconds)/60, int(seconds)%60, index), color.Black)
		var means [2][2]float64
		var counts [2]int
		wx, wy := xAt(.3127), yAt(.329)
		measurementLine(p.canvas, wx-5, wy, wx+5, wy, color.Gray{Y: 100})
		measurementLine(p.canvas, wx, wy-5, wx, wy+5, color.Gray{Y: 100})
		for source, points := range report.Automatic.Chromaticity {
			target := index
			if source == 1 && report.Options.Comparison != nil && report.Options.Comparison.FrameOffset != nil {
				target += *report.Options.Comparison.FrameOffset
			}
			ink := automaticBlue
			if source == 1 {
				ink = automaticGreen
			}
			for _, point := range points {
				if point.Index != target {
					continue
				}
				p.canvas.Set(xAt(point.X), yAt(point.Y), ink)
				counts[source]++
			}
			if counts[source] > 0 {
				frame, _ := automaticFrameAt(report, target, source == 1)
				means[source] = frame.ChromaticityMean
				x, y := xAt(means[source][0]), yAt(means[source][1])
				measurementLine(p.canvas, x-5, y, x+5, y, ink)
				measurementLine(p.canvas, x, y-5, x, y+5, ink)
				p.label(left+12, 154+source*22, fmt.Sprintf("%c mean (%.4f, %.4f)", 'A'+source, means[source][0], means[source][1]), ink)
			}
		}
		if counts[0] > 0 && counts[1] > 0 {
			p.label(left+12, 202, fmt.Sprintf("Delta xy = %.4f", math.Hypot(means[0][0]-means[1][0], means[0][1]-means[1][1])), color.Black)
		}
		p.label(left+12, 224, fmt.Sprintf("Retained points: A=%d B=%d", counts[0], counts[1]), color.Black)
	}
}

func selectAutomaticChromaticity(report *MeasurementReport, indices []int) []int {
	if len(indices) <= 5 {
		return indices
	}
	if len(report.Automatic.Reference) == 0 || len(report.Automatic.Chromaticity[0]) == 0 {
		return []int{indices[0], indices[(len(indices)-1)/4], indices[(len(indices)-1)/2], indices[3*(len(indices)-1)/4], indices[len(indices)-1]}
	}
	deviation := func(index int) float64 {
		slot := index
		if report.Automatic.Sampling != nil {
			slot, _ = slices.BinarySearchFunc(report.Automatic.Frames, index, func(frame AutomaticFrame, index int) int { return frame.Index - index })
		}
		values, available := automaticCurveValues(report, api.HDRArtifactAutomaticColour, slot)
		if !available {
			return -1
		}
		return max(math.Abs(values[0]), math.Abs(values[1]), math.Abs(values[2]))
	}
	ranked := slices.Clone(indices)
	slices.SortStableFunc(ranked, func(a, b int) int {
		if deviation(a) > deviation(b) {
			return -1
		}
		if deviation(a) < deviation(b) {
			return 1
		}
		return 0
	})
	selected := make([]int, 0, 5)
	for _, index := range ranked {
		if deviation(index) < 0 || slices.ContainsFunc(selected, func(other int) bool {
			frame, _ := automaticFrameAt(report, index, false)
			otherFrame, _ := automaticFrameAt(report, other, false)
			return math.Abs(automaticSeconds(frame)-automaticSeconds(otherFrame)) < 300
		}) {
			continue
		}
		selected = append(selected, index)
		if len(selected) == 4 {
			break
		}
	}
	for _, index := range slices.Backward(ranked) {
		if deviation(index) >= 0 && !slices.Contains(selected, index) {
			selected = append(selected, index)
			break
		}
	}
	slices.Sort(selected)
	return selected
}
