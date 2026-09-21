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
	analysis := newSpectrogramAnalysis(1, 48_000, frames)
	const bin = 32
	const amplitude = 0.5
	for frame := range int64(frames) {
		value := float32(amplitude * math.Sin(2*math.Pi*bin*float64(frame)/spectrogramFFTSize))
		analysis.add(frame, []float32{value})
	}
	analysis.finish(frames)
	column := spectrogramPlotWidth / 2
	got := analysis.decibels(0, column, bin)
	want := 20 * math.Log10(amplitude)
	if math.Abs(got-want) > 0.2 {
		t.Fatalf("tone power = %.3f dBFS, want %.3f", got, want)
	}
	if neighbor := analysis.decibels(0, column, bin+8); neighbor > -110 {
		t.Fatalf("distant leakage = %.3f dBFS, want <= -110", neighbor)
	}
}

func TestSpectrogramInteriorWindowUsesExactPCMAndWindow(t *testing.T) {
	analysis := newSpectrogramAnalysis(2, 48_000, 48_000*60)
	const start = int64(1020) // Cross the circular buffer boundary.
	available := start + spectrogramFFTSize
	samples := make([][]float32, 2)
	for channel := range samples {
		samples[channel] = make([]float32, spectrogramFFTSize)
		for index := range spectrogramFFTSize {
			frame := start + int64(index)
			value := float32(math.Sin(float64(frame+int64(channel)*37)*0.13) * 0.25)
			samples[channel][index] = value
			analysis.ring[channel][frame%spectrogramFFTSize] = float64(value)
		}
	}
	center := start + spectrogramFFTSize/2
	analysis.accumulateWindow(center, available)
	bucket := int(center / analysis.bucketFrames)
	if analysis.bucketCounts[bucket] != 1 {
		t.Fatalf("window count = %d, want 1", analysis.bucketCounts[bucket])
	}
	for channel := range samples {
		input := make([]float64, spectrogramFFTSize)
		output := make([]complex128, spectrogramBins)
		for index, sample := range samples[channel] {
			input[index] = float64(sample) * analysis.window[index]
		}
		analysis.plan.RFFT(output, input)
		for bin, value := range output {
			want := float32(real(value)*real(value) + imag(value)*imag(value))
			if got := analysis.bucketPower[channel][bucket*spectrogramBins+bin]; got != want {
				t.Fatalf("channel %d bin %d power = %g, want %g", channel, bin, got, want)
			}
		}
	}
}

func TestParallelSpectrogramPreservesSequentialChannelPower(t *testing.T) {
	for _, test := range []struct {
		name      string
		channels  int
		frames    int64
		estimated int64
	}{
		{
			name:      "stereo batch edge",
			channels:  2,
			frames:    66_000,
			estimated: 66_000,
		},
		{
			name:      "surround compacted columns",
			channels:  6,
			frames:    10_000,
			estimated: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sequential := newSpectrogramAnalysis(test.channels, 48_000, test.estimated)
			parallel := newParallelSpectrogram(test.channels, 48_000, test.estimated)
			defer parallel.close()
			samples := make([]float32, test.channels)
			for frame := range test.frames {
				for channel := range samples {
					samples[channel] = float32(math.Sin(float64(frame*int64(channel+1))*0.17) * 0.25)
				}
				sequential.add(frame, samples)
				parallel.add(frame, samples)
			}
			sequential.finish(test.frames)
			concurrent := parallel.finish(test.frames)
			if concurrent.bucketCount != sequential.bucketCount || !slices.Equal(concurrent.counts, sequential.counts) {
				t.Fatalf("column grouping differs: serial=%d parallel=%d", sequential.bucketCount, concurrent.bucketCount)
			}
			for channel := range test.channels {
				if !slices.Equal(concurrent.power[channel], sequential.power[channel]) {
					t.Fatalf("channel %d FFT power differs from sequential processing", channel)
				}
			}
		})
	}
}

func TestSpectrogramDCAndNyquistFollowSoxNormalization(t *testing.T) {
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
			wantDB: 20 * math.Log10(0.5),
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
			wantDB: 20 * math.Log10(0.5),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			const frames = 4096
			analysis := newSpectrogramAnalysis(1, 48_000, frames)
			for frame := range int64(frames) {
				analysis.add(frame, []float32{test.value(frame)})
			}
			analysis.finish(frames)
			got := analysis.decibels(0, spectrogramPlotWidth/2, test.bin)
			if math.Abs(got-test.wantDB) > 0.2 {
				t.Fatalf("power = %.3f dBFS, want %.3f", got, test.wantDB)
			}
		})
	}
}

func TestSpectrogramUsesSoxOverlapAndBoundedBuckets(t *testing.T) {
	t.Parallel()

	total := int64(48_000 * 2 * 60 * 60)
	analysis := newSpectrogramAnalysis(1, 48_000, total)
	if math.Abs(analysis.windowSum/float64(spectrogramFFTSize)-0.334927765936879) > 1e-9 {
		t.Fatalf("SoX Kaiser window density = %.12f, want 0.334927765937", analysis.windowSum/float64(spectrogramFFTSize))
	}
	if analysis.bucketLimit != spectrogramPlotWidth {
		t.Fatalf("spectrogram bucket limit = %d, want %d columns", analysis.bucketLimit, spectrogramPlotWidth)
	}
	if analysis.hop != 343 {
		t.Fatalf("spectrogram hop = %d frames, want SoX Kaiser overlap of 343", analysis.hop)
	}
	if analysis.blockSteps != 336 || analysis.bucketFrames != 115_248 {
		t.Fatalf("spectrogram column = %d windows / %d frames, want 336 / 115248", analysis.blockSteps, analysis.bucketFrames)
	}
	if windows := (total + analysis.hop - 1) / analysis.hop; windows < 1_000_000 {
		t.Fatalf("FFT windows = %d, want dense SoX-style overlap", windows)
	}
	shortTotal := int64(48_000 * 60)
	shortAnalysis := newSpectrogramAnalysis(1, 48_000, shortTotal)
	if shortAnalysis.hop != 320 {
		t.Fatalf("short-source hop = %d frames, want 320", shortAnalysis.hop)
	}
	if shortAnalysis.blockSteps != 3 || shortAnalysis.bucketFrames != 960 {
		t.Fatalf("short-source column = %d windows / %d frames, want 3 / 960", shortAnalysis.blockSteps, shortAnalysis.bucketFrames)
	}
	subsecond := newSpectrogramAnalysis(1, 48_000, 9_600)
	if subsecond.hop != 9 || subsecond.blockSteps != 1 {
		t.Fatalf("subsecond SoX cap = hop %d / steps %d, want 9 / 1", subsecond.hop, subsecond.blockSteps)
	}
}

func TestSpectrogramGroupsExactlySoxWindowsPerColumn(t *testing.T) {
	t.Parallel()

	analysis := newSpectrogramAnalysis(1, 48_000, 308_467_712)
	if analysis.hop != 343 || analysis.blockSteps != 300 || analysis.bucketFrames != 102_900 {
		t.Fatalf("full-source geometry = hop %d, steps %d, column frames %d; want 343, 300, 102900",
			analysis.hop, analysis.blockSteps, analysis.bucketFrames)
	}
	for window := range analysis.blockSteps + 1 {
		center := analysis.hop/2 + window*analysis.hop
		analysis.accumulateWindow(center, center+spectrogramFFTSize/2)
	}
	if analysis.bucketCount != 2 || analysis.bucketCounts[0] != 300 || analysis.bucketCounts[1] != 1 {
		t.Fatalf("window grouping = buckets %d, counts %d/%d; want 2, 300/1",
			analysis.bucketCount, analysis.bucketCounts[0], analysis.bucketCounts[1])
	}
}

func TestSpectrogramExactDurationKeepsRoundedNativeColumns(t *testing.T) {
	t.Parallel()

	const total = int64(3_000_000)
	analysis := newSpectrogramAnalysis(1, 48_000, total)
	if analysis.hop != 333 || analysis.blockSteps != 3 || analysis.bucketFrames != 999 {
		t.Fatalf("native column geometry = hop %d steps %d frames %d, want 333/3/999",
			analysis.hop, analysis.blockSteps, analysis.bucketFrames)
	}
	if analysis.bucketLimit < 3003 {
		t.Fatalf("native bucket capacity = %d, want at least 3003", analysis.bucketLimit)
	}
	firstOverflowCenter := analysis.next + (3000*analysis.bucketFrames-analysis.next+analysis.hop-1)/analysis.hop*analysis.hop
	lastCenter := analysis.next + (total-1-analysis.next)/analysis.hop*analysis.hop
	for _, center := range []int64{firstOverflowCenter, lastCenter} {
		analysis.accumulateWindow(center, min(total, center+spectrogramFFTSize/2))
	}
	if analysis.bucketFrames != 999 || analysis.bucketCount != 3003 ||
		analysis.bucketCounts[3000] != 1 || analysis.bucketCounts[3002] != 1 {
		t.Fatalf("exact-duration native columns collapsed: frames=%d count=%d last=%d/%d",
			analysis.bucketFrames, analysis.bucketCount, analysis.bucketCounts[3000], analysis.bucketCounts[3002])
	}
}

func TestSpectrogramSoxEdgeWindowAdjustment(t *testing.T) {
	t.Parallel()

	analysis := newSpectrogramAnalysis(1, 48_000, 144_000)
	for _, test := range []struct {
		name      string
		start     int64
		available int64
		left      int
		span      int
	}{
		{
			name:      "leading",
			start:     -488,
			available: 536,
			left:      488,
			span:      536,
		},
		{
			name:      "trailing",
			start:     100,
			available: 700,
			left:      0,
			span:      600,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			window := analysis.windowForRange(test.start, test.available)
			sum := 0.0
			for index, weight := range window {
				if (index < test.left || index >= test.left+test.span) && weight != 0 {
					t.Fatalf("out-of-range weight at %d = %g", index, weight)
				}
				sum += weight
			}
			fraction := float64(test.span) / spectrogramFFTSize
			want := 2 * fraction * fraction
			if math.Abs(sum-want) > 1e-12 {
				t.Fatalf("edge window sum = %.12f, want %.12f", sum, want)
			}
		})
	}
}

func TestAnalysesRemapUsingDecodedDuration(t *testing.T) {
	t.Parallel()

	const longFrames = int64(1_600_000)
	for _, test := range []struct{ estimated, frames int64 }{
		{estimated: 0, frames: 9_000},
		{estimated: longFrames / 4, frames: longFrames},
		{estimated: longFrames * 4, frames: longFrames},
	} {
		estimated, frames := test.estimated, test.frames
		waveform := newWaveformAnalysis(1)
		spectrogram := newSpectrogramAnalysis(1, 48_000, estimated)
		for frame := range frames {
			sample := float32(0.25)
			waveform.add(frame, []float32{sample})
			spectrogram.add(frame, []float32{sample})
		}
		waveform.finish(frames)
		spectrogram.finish(frames)
		if !waveform.seen[0] || !waveform.seen[waveformPlotWidth-1] {
			t.Fatalf("estimate %d left waveform edge unmapped", estimated)
		}
		if spectrogram.total != frames || spectrogram.decibels(0, 0, 0) <= spectrogramFloorDB ||
			spectrogram.decibels(0, spectrogramPlotWidth-1, 0) <= spectrogramFloorDB {
			t.Fatalf("estimate %d produced incomplete spectrogram mapping: total=%d native_columns=%d",
				estimated, spectrogram.total, spectrogram.bucketCount)
		}
		if spectrogram.bucketCount > spectrogram.bucketLimit {
			t.Fatalf("estimate %d retained %d buckets, limit %d", estimated, spectrogram.bucketCount, spectrogram.bucketLimit)
		}
	}
}

func TestSpectrogramCompactionClearsReusedBuckets(t *testing.T) {
	analysis := newSpectrogramAnalysis(1, 48_000, 1)
	analysis.bucketLimit = 4
	analysis.bucketFrames = 1
	analysis.bucketCount = 4
	for bucket := range analysis.bucketCount {
		analysis.bucketCounts[bucket] = 1
		analysis.bucketPower[0][bucket*spectrogramBins+8] = 1
	}
	analysis.compact()
	analysis.accumulateWindow(5, 5)
	if got := analysis.bucketCounts[2]; got != 1 {
		t.Fatalf("reused bucket window count = %d, want 1", got)
	}
	if got := analysis.bucketPower[0][2*spectrogramBins+8]; got != 0 {
		t.Fatalf("reused bucket power = %v, want 0", got)
	}
}

func TestWaveformExtremaAndCanvasGeometry(t *testing.T) {
	analysis := newWaveformAnalysis(6)
	for frame, value := range []float32{-1, 0.25, 0.75, -0.5} {
		samples := []float32{value, value / 2, 0, -value, 0.1, -0.1}
		analysis.add(int64(frame), samples)
	}
	analysis.finish(4)
	if analysis.minimum[0][0] != -1 || analysis.maximum[0][waveformPlotWidth/2] != 0.75 {
		t.Fatalf("unexpected extrema: first=%v middle=%v", analysis.minimum[0][0], analysis.maximum[0][waveformPlotWidth/2])
	}
	waveform := renderWaveform(analysis, 48_000, 4, "5.1(side)")
	if got := waveform.Bounds().Size(); got.X != 1812 || got.Y != 980 {
		t.Fatalf("waveform size = %v", got)
	}
	spectrogram := newSpectrogramAnalysis(6, 48_000, 1)
	spectrogram.add(0, make([]float32, 6))
	spectrogram.finish(1)
	image := renderSpectrogram(spectrogram, 48_000, "5.1(side)")
	if got := image.Bounds().Size(); got.X != 3144 || got.Y != 3161 {
		t.Fatalf("spectrogram size = %v", got)
	}
}

func TestWaveformDBLabelsFollowRenderedAmplitudeScale(t *testing.T) {
	t.Parallel()

	analysis := newWaveformAnalysis(1)
	analysis.add(0, []float32{0})
	analysis.finish(1)
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
	analysis.add(0, []float32{1, 0})
	analysis.add(1, []float32{-1, 0.5})
	analysis.finish(2)
	if analysis.maximum[0][0] != 1 || analysis.minimum[0][waveformPlotWidth/2] != -1 {
		t.Fatalf("left impulses were not preserved: min=%v max=%v", analysis.minimum[0], analysis.maximum[0])
	}
	if analysis.maximum[1][0] != 0 || analysis.maximum[1][waveformPlotWidth/2] != 0.5 || analysis.minimum[1][waveformPlotWidth/2] != 0.5 {
		t.Fatalf("right channel leaked or lost its impulse")
	}
}

func TestWaveformMarksFullScaleClippingByChannel(t *testing.T) {
	analysis := newWaveformAnalysis(2)
	analysis.add(0, []float32{1, 0.5})
	analysis.add(1, []float32{0, 0})
	analysis.finish(2)
	canvas := renderWaveform(analysis, 48_000, 2, "stereo")
	if got := canvas.At(0, waveformPanelHeight/2); got != clipRed {
		t.Fatalf("clipped channel pixel = %v, want %v", got, clipRed)
	}
	if got := canvas.At(0, waveformPanelHeight+waveformPanelHeight/2); got == clipRed {
		t.Fatalf("unclipped channel marked as clipped")
	}
}

func absoluteByteDifference(left uint8, right uint8) int {
	return int(max(left, right) - min(left, right))
}
