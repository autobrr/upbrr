// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"math"
	"slices"
	"testing"
)

func TestSpectrogramBinCenteredTonePower(t *testing.T) {
	const frames = 8192
	analysis := newSpectrogramAnalysis(1, frames)
	const bin = 32
	const amplitude = 0.5
	for frame := range int64(frames) {
		value := float32(amplitude * math.Sin(2*math.Pi*bin*float64(frame)/spectrogramFFTSize))
		analysis.add(frame, []float32{value})
	}
	analysis.finish()
	column := spectrogramPlotWidth / 2
	got := analysis.decibels(0, column, bin)
	want := 20 * math.Log10(amplitude)
	if math.Abs(got-want) > 0.2 {
		t.Fatalf("tone power = %.3f dBFS, want %.3f", got, want)
	}
	if neighbor := analysis.decibels(0, column, bin+4); neighbor > -70 {
		t.Fatalf("distant leakage = %.3f dBFS, want <= -70", neighbor)
	}
}

func TestSpectrogramDCAndNyquistNormalization(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  func(int64) float32
		bin    int
		wantDB float64
	}{
		{
			name:   "dc",
			value:  func(int64) float32 { return 0.25 },
			bin:    0,
			wantDB: 20 * math.Log10(0.25),
		},
		{
			name: "nyquist",
			value: func(frame int64) float32 {
				if frame%2 == 0 {
					return 0.25
				}
				return -0.25
			},
			bin:    spectrogramBins - 1,
			wantDB: 20 * math.Log10(0.25),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			const frames = 4096
			analysis := newSpectrogramAnalysis(1, frames)
			for frame := range int64(frames) {
				analysis.add(frame, []float32{test.value(frame)})
			}
			analysis.finish()
			got := analysis.decibels(0, spectrogramPlotWidth/2, test.bin)
			if math.Abs(got-test.wantDB) > 0.2 {
				t.Fatalf("power = %.3f dBFS, want %.3f", got, test.wantDB)
			}
		})
	}
}

func TestWaveformExtremaAndCanvasGeometry(t *testing.T) {
	analysis := newWaveformAnalysis(6)
	for frame, value := range []float32{-1, 0.25, 0.75, -0.5} {
		samples := []float32{value, value / 2, 0, -value, 0.1, -0.1}
		analysis.add(int64(frame), 4, samples)
	}
	analysis.finish()
	if analysis.minimum[0][0] != -1 || analysis.maximum[0][waveformPlotWidth/2] != 0.75 {
		t.Fatalf("unexpected extrema: first=%v middle=%v", analysis.minimum[0][0], analysis.maximum[0][waveformPlotWidth/2])
	}
	waveform := renderWaveform(analysis, 48_000, 4, "5.1(side)")
	if got := waveform.Bounds().Size(); got.X != 1812 || got.Y != 980 {
		t.Fatalf("waveform size = %v", got)
	}
	spectrogram := newSpectrogramAnalysis(6, 1)
	spectrogram.add(0, make([]float32, 6))
	spectrogram.finish()
	image := renderSpectrogram(spectrogram, 48_000, "5.1(side)")
	if got := image.Bounds().Size(); got.X != 3141 || got.Y != 3161 {
		t.Fatalf("spectrogram size = %v", got)
	}
}

func TestWaveformDBLabelsFollowRenderedAmplitudeScale(t *testing.T) {
	t.Parallel()

	analysis := newWaveformAnalysis(1)
	analysis.add(0, 1, []float32{0})
	analysis.finish()
	canvas := renderWaveform(analysis, 48_000, 1, "mono")
	center := waveformPanelHeight / 2
	scale := float64(waveformPanelHeight/2 - 2)
	wantTopRows := []int{
		center - int(math.Round(math.Pow(10, -2.0/20)*scale)) - 3,
		center - int(math.Round(math.Pow(10, -10.0/20)*scale)) - 3,
		center - 3,
		center + int(math.Round(math.Pow(10, -10.0/20)*scale)) - 3,
		center + int(math.Round(math.Pow(10, -2.0/20)*scale)) - 3,
	}
	for _, top := range wantTopRows {
		found := false
		for y := top; y < top+7 && !found; y++ {
			for x := waveformPlotWidth + 4; x < canvas.Bounds().Dx(); x++ {
				if canvas.At(x, y) == textGray {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("no dB label pixels at independently calculated rows %d-%d", top, top+6)
		}
	}
}

func TestSpectrogramPaletteCalibration(t *testing.T) {
	for _, test := range []struct {
		decibels float64
		red      uint8
		green    uint8
		blue     uint8
	}{
		{
			decibels: -120,
			red:      0,
			green:    0,
			blue:     0,
		},
		{
			decibels: 0,
			red:      255,
			green:    255,
			blue:     255,
		},
		{
			decibels: -60,
			red:      210,
			green:    0,
			blue:     64,
		},
	} {
		got := spectrogramColor(test.decibels)
		if absoluteByteDifference(got.R, test.red) > 3 || absoluteByteDifference(got.G, test.green) > 3 || absoluteByteDifference(got.B, test.blue) > 3 {
			t.Fatalf("color(%v) = %#v, want near rgb(%d,%d,%d)", test.decibels, got, test.red, test.green, test.blue)
		}
	}
}

func TestChannelLabelsDistinguishSideBackAndPreserveEightChannelOrder(t *testing.T) {
	tests := []struct {
		layout   string
		channels int
		want     []string
	}{
		{
			layout:   "5.1(side)",
			channels: 6,
			want:     []string{"L", "R", "C", "LFE", "SL", "SR"},
		},
		{
			layout:   "5.1",
			channels: 6,
			want:     []string{"L", "R", "C", "LFE", "BL", "BR"},
		},
		{
			layout:   "7.1",
			channels: 8,
			want:     []string{"L", "R", "C", "LFE", "BL", "BR", "SL", "SR"},
		},
	}
	for _, test := range tests {
		if got := channelLabels(test.layout, test.channels); !slices.Equal(got, test.want) {
			t.Fatalf("channelLabels(%q, %d) = %v, want %v", test.layout, test.channels, got, test.want)
		}
	}
}

func TestWaveformKeepsSignedImpulsesIsolatedByChannel(t *testing.T) {
	analysis := newWaveformAnalysis(2)
	analysis.add(0, 2, []float32{1, 0})
	analysis.add(1, 2, []float32{-1, 0.5})
	analysis.finish()
	if analysis.maximum[0][0] != 1 || analysis.minimum[0][waveformPlotWidth/2] != -1 {
		t.Fatalf("left impulses were not preserved: min=%v max=%v", analysis.minimum[0], analysis.maximum[0])
	}
	if analysis.maximum[1][0] != 0 || analysis.maximum[1][waveformPlotWidth/2] != 0.5 || analysis.minimum[1][waveformPlotWidth/2] != 0.5 {
		t.Fatalf("right channel leaked or lost its impulse")
	}
}

func absoluteByteDifference(left uint8, right uint8) int {
	return int(max(left, right) - min(left, right))
}
