// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
)

const (
	waveformPanelHeight     = 160
	waveformAxisHeight      = 20
	waveformLabelWidth      = 42
	spectrogramPanelHeight  = spectrogramBins
	spectrogramTopMargin    = 30
	spectrogramBottomMargin = 48
	spectrogramLeftMargin   = 58
	spectrogramAxisGap      = 37
	spectrogramLegendWidth  = 14
	spectrogramRightMargin  = 35
)

var (
	canvasBlack = color.RGBA{A: 255}
	waveBlue    = color.RGBA{
		R: 47,
		G: 135,
		B: 255,
		A: 255,
	}
	labelBlack = color.RGBA{
		R: 19,
		G: 22,
		B: 26,
		A: 255,
	}
	borderGray = color.RGBA{
		R: 71,
		G: 79,
		B: 85,
		A: 255,
	}
	dividerGray = color.RGBA{
		R: 86,
		G: 96,
		B: 104,
		A: 255,
	}
	textGray = color.RGBA{
		R: 185,
		G: 190,
		B: 194,
		A: 255,
	}
)

func renderWaveform(analysis *waveformAnalysis, sampleRate int, frames int64, layout string) image.Image {
	channels := len(analysis.minimum)
	height := channels*waveformPanelHeight + waveformAxisHeight
	canvas := image.NewRGBA(image.Rect(0, 0, waveformPlotWidth+waveformLabelWidth, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(canvasBlack), image.Point{}, draw.Src)
	labels := channelLabels(layout, channels)
	for channel := range channels {
		top := channel * waveformPanelHeight
		fillRect(canvas, image.Rect(waveformPlotWidth, top, canvas.Bounds().Dx(), top+waveformPanelHeight), labelBlack)
		drawHorizontal(canvas, top, dividerGray)
		center := top + waveformPanelHeight/2
		drawHorizontalRange(canvas, center, 0, waveformPlotWidth, borderGray)
		for column := range waveformPlotWidth {
			minimum := max(-1, min(1, float64(analysis.minimum[channel][column])))
			maximum := max(-1, min(1, float64(analysis.maximum[channel][column])))
			yTop := waveformAmplitudeY(center, maximum)
			yBottom := waveformAmplitudeY(center, minimum)
			drawVertical(canvas, column, min(yTop, yBottom), max(yTop, yBottom), waveBlue)
		}
		drawText(canvas, waveformPlotWidth+4, top+4, labels[channel], textGray)
		drawWaveformDBLabel(canvas, center, -2, -1)
		drawWaveformDBLabel(canvas, center, -10, -1)
		drawText(canvas, waveformPlotWidth+4, center-3, "-INF", textGray)
		drawWaveformDBLabel(canvas, center, -10, 1)
		drawWaveformDBLabel(canvas, center, -2, 1)
	}
	drawHorizontal(canvas, channels*waveformPanelHeight, dividerGray)
	duration := float64(frames) / float64(sampleRate)
	for tick := 0; tick <= 10; tick++ {
		x := tick * (waveformPlotWidth - 1) / 10
		drawVertical(canvas, x, channels*waveformPanelHeight, channels*waveformPanelHeight+3, borderGray)
		label := formatElapsed(duration * float64(tick) / 10)
		drawText(canvas, max(0, min(x-len(label)*3, waveformPlotWidth-len(label)*6)), channels*waveformPanelHeight+7, label, textGray)
	}
	return canvas
}

func waveformAmplitudeY(center int, amplitude float64) int {
	return center - int(math.Round(amplitude*float64(waveformPanelHeight/2-2)))
}

func drawWaveformDBLabel(canvas *image.RGBA, center int, decibels float64, direction float64) {
	amplitude := direction * math.Pow(10, decibels/20)
	drawText(canvas, waveformPlotWidth+4, waveformAmplitudeY(center, amplitude)-3, strconv.Itoa(int(decibels)), textGray)
}

func renderSpectrogram(analysis *spectrogramAnalysis, sampleRate int, layout string) image.Image {
	channels := analysis.channels
	width := spectrogramLeftMargin + spectrogramPlotWidth + spectrogramAxisGap + spectrogramLegendWidth + spectrogramRightMargin
	height := spectrogramTopMargin + channels*spectrogramPanelHeight + max(0, channels-1) + spectrogramBottomMargin
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(canvasBlack), image.Point{}, draw.Src)
	labels := channelLabels(layout, channels)
	for channel := range channels {
		top := spectrogramTopMargin + channel*(spectrogramPanelHeight+1)
		for column := range spectrogramPlotWidth {
			for row := range spectrogramPanelHeight {
				bin := spectrogramBins - 1 - row
				canvas.SetRGBA(spectrogramLeftMargin+column, top+row, spectrogramColor(analysis.decibels(channel, column, bin)))
			}
		}
		if channel+1 < channels {
			drawHorizontalRange(canvas, top+spectrogramPanelHeight, spectrogramLeftMargin, spectrogramLeftMargin+spectrogramPlotWidth, dividerGray)
		}
		drawText(canvas, 4, top+4, labels[channel], textGray)
		for _, tick := range []int{0, 5_000, 10_000, 15_000, 20_000} {
			if tick > sampleRate/2 {
				continue
			}
			y := top + spectrogramPanelHeight - 1 - int(math.Round(float64(tick)*float64(spectrogramPanelHeight-1)/float64(sampleRate/2)))
			label := strconv.Itoa(tick / 1000)
			if tick == 0 {
				label = "0"
			}
			drawHorizontalRange(canvas, y, spectrogramLeftMargin-4, spectrogramLeftMargin, borderGray)
			drawText(canvas, max(1, spectrogramLeftMargin-7-len(label)*6), y-3, label, textGray)
		}
	}
	legendX := spectrogramLeftMargin + spectrogramPlotWidth + spectrogramAxisGap
	legendTop := spectrogramTopMargin
	legendBottom := height - spectrogramBottomMargin
	for y := legendTop; y < legendBottom; y++ {
		fraction := 1 - float64(y-legendTop)/float64(max(1, legendBottom-legendTop-1))
		fillRect(canvas, image.Rect(legendX, y, legendX+spectrogramLegendWidth, y+1), spectrogramColor(spectrogramFloorDB+fraction*-spectrogramFloorDB))
	}
	for _, value := range []int{0, -20, -40, -60, -80, -100, -120} {
		y := legendTop + int(float64(-value)/120*float64(legendBottom-legendTop-1))
		drawText(canvas, legendX+spectrogramLegendWidth+4, y-3, strconv.Itoa(value), textGray)
	}
	drawText(canvas, legendX+spectrogramLegendWidth+4, max(0, legendTop-15), "DBFS", textGray)
	for tick := 0; tick <= 10; tick++ {
		x := spectrogramLeftMargin + tick*(spectrogramPlotWidth-1)/10
		drawVertical(canvas, x, height-spectrogramBottomMargin, height-spectrogramBottomMargin+4, borderGray)
		label := formatElapsed(float64(analysis.total) / float64(sampleRate) * float64(tick) / 10)
		drawText(
			canvas,
			max(spectrogramLeftMargin, min(x-len(label)*3, spectrogramLeftMargin+spectrogramPlotWidth-len(label)*6)),
			height-30,
			label,
			textGray,
		)
	}
	return canvas
}

func spectrogramColor(decibels float64) color.RGBA {
	x := min(1, max(0, (decibels-spectrogramFloorDB)/-spectrogramFloorDB))
	var red, green, blue float64
	if x >= 0.13 {
		if x < 0.73 {
			red = math.Sin((x - 0.13) / 0.60 * math.Pi / 2)
		} else {
			red = 1
		}
	}
	if x >= 0.60 {
		if x < 0.91 {
			green = math.Sin((x - 0.60) / 0.31 * math.Pi / 2)
		} else {
			green = 1
		}
	}
	if x < 0.60 {
		blue = 0.5 * math.Sin(x/0.60*math.Pi)
	} else if x >= 0.78 {
		blue = (x - 0.78) / 0.22
	}
	return color.RGBA{
		R: uint8(math.Round(255 * red)),
		G: uint8(math.Round(255 * green)),
		B: uint8(math.Round(255 * blue)),
		A: 255,
	}
}

func channelLabels(layout string, channels int) []string {
	normalized := strings.ToLower(strings.TrimSpace(layout))
	var labels []string
	switch {
	case channels == 1:
		labels = []string{"C"}
	case channels == 2:
		labels = []string{"L", "R"}
	case channels == 6 && strings.Contains(normalized, "side"):
		labels = []string{"L", "R", "C", "LFE", "SL", "SR"}
	case channels == 6:
		labels = []string{"L", "R", "C", "LFE", "BL", "BR"}
	case channels == 8:
		labels = []string{"L", "R", "C", "LFE", "BL", "BR", "SL", "SR"}
	default:
		labels = make([]string, channels)
		for index := range channels {
			labels[index] = fmt.Sprintf("CH%d", index+1)
		}
	}
	return labels
}

func formatElapsed(seconds float64) string {
	total := max(0, int(math.Round(seconds)))
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func fillRect(canvas *image.RGBA, rectangle image.Rectangle, value color.RGBA) {
	draw.Draw(canvas, rectangle, image.NewUniform(value), image.Point{}, draw.Src)
}

func drawHorizontal(canvas *image.RGBA, y int, value color.RGBA) {
	drawHorizontalRange(canvas, y, 0, canvas.Bounds().Dx(), value)
}

func drawHorizontalRange(canvas *image.RGBA, y int, left int, right int, value color.RGBA) {
	if y < 0 || y >= canvas.Bounds().Dy() {
		return
	}
	for x := max(0, left); x < min(right, canvas.Bounds().Dx()); x++ {
		canvas.SetRGBA(x, y, value)
	}
}

func drawVertical(canvas *image.RGBA, x int, top int, bottom int, value color.RGBA) {
	if x < 0 || x >= canvas.Bounds().Dx() {
		return
	}
	for y := max(0, top); y <= min(bottom, canvas.Bounds().Dy()-1); y++ {
		canvas.SetRGBA(x, y, value)
	}
}

// bitmapGlyphs is an original compact 5x7 ASCII subset used for deterministic
// numeric, unit, and channel labels without runtime font discovery.
var bitmapGlyphs = map[rune][7]byte{
	'0': {14, 17, 19, 21, 25, 17, 14},
	'1': {4, 12, 4, 4, 4, 4, 14},
	'2': {14, 17, 1, 2, 4, 8, 31},
	'3': {30, 1, 1, 14, 1, 1, 30},
	'4': {2, 6, 10, 18, 31, 2, 2},
	'5': {31, 16, 16, 30, 1, 1, 30},
	'6': {14, 16, 16, 30, 17, 17, 14},
	'7': {31, 1, 2, 4, 8, 8, 8},
	'8': {14, 17, 17, 14, 17, 17, 14},
	'9': {14, 17, 17, 15, 1, 1, 14},
	':': {0, 4, 4, 0, 4, 4, 0},
	'-': {0, 0, 0, 31, 0, 0, 0},
	'.': {0, 0, 0, 0, 0, 4, 4},
	'A': {14, 17, 17, 31, 17, 17, 17},
	'B': {30, 17, 17, 30, 17, 17, 30},
	'C': {14, 17, 16, 16, 16, 17, 14},
	'D': {30, 17, 17, 17, 17, 17, 30},
	'E': {31, 16, 16, 30, 16, 16, 31},
	'F': {31, 16, 16, 30, 16, 16, 16},
	'H': {17, 17, 17, 31, 17, 17, 17},
	'I': {14, 4, 4, 4, 4, 4, 14},
	'L': {16, 16, 16, 16, 16, 16, 31},
	'N': {17, 25, 25, 21, 19, 19, 17},
	'R': {30, 17, 17, 30, 20, 18, 17},
	'S': {15, 16, 16, 14, 1, 1, 30},
	'T': {31, 4, 4, 4, 4, 4, 4},
	'K': {17, 18, 20, 24, 20, 18, 17},
}

func drawText(canvas *image.RGBA, x int, y int, value string, foreground color.RGBA) {
	for _, character := range strings.ToUpper(value) {
		glyph, ok := bitmapGlyphs[character]
		if !ok {
			x += 6
			continue
		}
		for row, bits := range glyph {
			for column := range 5 {
				if bits&(1<<uint(4-column)) == 0 {
					continue
				}
				fillRect(canvas, image.Rect(x+column, y+row, x+column+1, y+row+1), foreground)
			}
		}
		x += 6
	}
}
