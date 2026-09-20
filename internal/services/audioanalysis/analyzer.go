// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"math"

	"github.com/go-fft/fft"
)

const (
	waveformPlotWidth    = 1770
	spectrogramPlotWidth = 2997
	spectrogramFFTSize   = 1024
	spectrogramBins      = spectrogramFFTSize/2 + 1
	spectrogramMaxHop    = 512
	spectrogramFloorDB   = -120.0
)

type waveformAnalysis struct {
	minimum [][]float32
	maximum [][]float32
	seen    []bool
}

func newWaveformAnalysis(channels int) *waveformAnalysis {
	analysis := &waveformAnalysis{
		minimum: make([][]float32, channels),
		maximum: make([][]float32, channels),
		seen:    make([]bool, waveformPlotWidth),
	}
	for channel := range channels {
		analysis.minimum[channel] = make([]float32, waveformPlotWidth)
		analysis.maximum[channel] = make([]float32, waveformPlotWidth)
		for column := range waveformPlotWidth {
			analysis.minimum[channel][column] = float32(math.Inf(1))
			analysis.maximum[channel][column] = float32(math.Inf(-1))
		}
	}
	return analysis
}

func (a *waveformAnalysis) add(frame int64, total int64, samples []float32) {
	column := int(frame * waveformPlotWidth / total)
	column = min(column, waveformPlotWidth-1)
	a.seen[column] = true
	for channel, sample := range samples {
		a.minimum[channel][column] = min(a.minimum[channel][column], sample)
		a.maximum[channel][column] = max(a.maximum[channel][column], sample)
	}
}

func (a *waveformAnalysis) finish() {
	for column := range waveformPlotWidth {
		if a.seen[column] {
			continue
		}
		for channel := range a.minimum {
			a.minimum[channel][column] = 0
			a.maximum[channel][column] = 0
		}
	}
}

type spectrogramAnalysis struct {
	channels  int
	total     int64
	hop       int64
	next      int64
	window    []float64
	windowSum float64
	plan      *fft.RealPlan
	ring      [][]float64
	power     [][]float64
	counts    []uint32
	fftInput  []float64
	fftOutput []complex128
}

func newSpectrogramAnalysis(channels int, total int64) *spectrogramAnalysis {
	hop := (total + spectrogramPlotWidth - 1) / spectrogramPlotWidth
	hop = min(max(hop, 1), int64(spectrogramMaxHop))
	analysis := &spectrogramAnalysis{
		channels:  channels,
		total:     total,
		hop:       hop,
		window:    make([]float64, spectrogramFFTSize),
		plan:      fft.NewRealPlan(spectrogramFFTSize),
		ring:      make([][]float64, channels),
		power:     make([][]float64, channels),
		counts:    make([]uint32, spectrogramPlotWidth),
		fftInput:  make([]float64, spectrogramFFTSize),
		fftOutput: make([]complex128, spectrogramBins),
	}
	for index := range spectrogramFFTSize {
		analysis.window[index] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(index)/float64(spectrogramFFTSize-1))
		analysis.windowSum += analysis.window[index]
	}
	for channel := range channels {
		analysis.ring[channel] = make([]float64, spectrogramFFTSize)
		analysis.power[channel] = make([]float64, spectrogramPlotWidth*spectrogramBins)
	}
	return analysis
}

func (a *spectrogramAnalysis) add(frame int64, samples []float32) {
	for channel, sample := range samples {
		a.ring[channel][frame%spectrogramFFTSize] = float64(sample)
	}
	half := int64(spectrogramFFTSize / 2)
	for a.next < a.total && frame >= a.next+half-1 {
		a.accumulateWindow(a.next, frame+1)
		a.next += a.hop
	}
}

func (a *spectrogramAnalysis) finish() {
	for a.next < a.total {
		a.accumulateWindow(a.next, a.total)
		a.next += a.hop
	}
}

func (a *spectrogramAnalysis) accumulateWindow(center int64, available int64) {
	half := int64(spectrogramFFTSize / 2)
	start := center - half
	columnStart := int(center * spectrogramPlotWidth / a.total)
	columnEnd := int(min(a.total, center+a.hop)*spectrogramPlotWidth/a.total) - 1
	columnStart = min(max(columnStart, 0), spectrogramPlotWidth-1)
	columnEnd = min(max(columnEnd, columnStart), spectrogramPlotWidth-1)
	for column := columnStart; column <= columnEnd; column++ {
		a.counts[column]++
	}
	interiorDenominator := (a.windowSum / 2) * (a.windowSum / 2)
	edgeDenominator := a.windowSum * a.windowSum
	for channel := range a.channels {
		clear(a.fftInput)
		for index := range spectrogramFFTSize {
			source := start + int64(index)
			if source >= 0 && source < available && source < a.total && available-source <= spectrogramFFTSize {
				a.fftInput[index] = a.ring[channel][source%spectrogramFFTSize] * a.window[index]
			}
		}
		a.plan.RFFT(a.fftOutput, a.fftInput)
		for bin, value := range a.fftOutput {
			denominator := interiorDenominator
			if bin == 0 || bin == spectrogramBins-1 {
				denominator = edgeDenominator
			}
			binPower := (real(value)*real(value) + imag(value)*imag(value)) / denominator
			for column := columnStart; column <= columnEnd; column++ {
				a.power[channel][column*spectrogramBins+bin] += binPower
			}
		}
	}
}

func (a *spectrogramAnalysis) decibels(channel int, column int, bin int) float64 {
	if channel < 0 || channel >= a.channels || column < 0 || column >= spectrogramPlotWidth || bin < 0 || bin >= spectrogramBins {
		return spectrogramFloorDB
	}
	count := a.counts[column]
	if count == 0 {
		return spectrogramFloorDB
	}
	power := a.power[channel][column*spectrogramBins+bin] / float64(count)
	if power <= 0 || math.IsNaN(power) || math.IsInf(power, 0) {
		return spectrogramFloorDB
	}
	return max(spectrogramFloorDB, min(0, 10*math.Log10(power)))
}
