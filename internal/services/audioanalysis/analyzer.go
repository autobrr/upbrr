// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"math"
	"runtime"
	"sync"

	"github.com/go-fft/fft"
)

const (
	waveformPlotWidth      = 1770
	waveformBucketCapacity = waveformPlotWidth * 4
	spectrogramPlotWidth   = 3000
	spectrogramFFTSize     = 1024
	spectrogramBins        = spectrogramFFTSize/2 + 1
	spectrogramFloorDB     = -120.0
	// SoX -w Kaiser -z 120 with its default window adjustment uses beta(132 dB, 0.1).
	spectrogramKaiserBeta = 13.740533666337532
)

type waveformAnalysis struct {
	channels        int
	bucketFrames    int64
	bucketCount     int
	bucketMinimum   [][]float32
	bucketMaximum   [][]float32
	bucketClipFirst [][]int64
	bucketClipLast  [][]int64
	minimum         [][]float32
	maximum         [][]float32
	clipped         [][]bool
	seen            []bool
}

func newWaveformAnalysis(channels int) *waveformAnalysis {
	analysis := &waveformAnalysis{
		channels:        channels,
		bucketFrames:    1,
		bucketMinimum:   make([][]float32, channels),
		bucketMaximum:   make([][]float32, channels),
		bucketClipFirst: make([][]int64, channels),
		bucketClipLast:  make([][]int64, channels),
	}
	for channel := range channels {
		analysis.bucketMinimum[channel] = make([]float32, waveformBucketCapacity)
		analysis.bucketMaximum[channel] = make([]float32, waveformBucketCapacity)
		analysis.bucketClipFirst[channel] = make([]int64, waveformBucketCapacity)
		analysis.bucketClipLast[channel] = make([]int64, waveformBucketCapacity)
	}
	return analysis
}

func (a *waveformAnalysis) add(frame int64, samples []float32) {
	for frame/a.bucketFrames >= waveformBucketCapacity {
		a.compact()
	}
	bucket := int(frame / a.bucketFrames)
	for a.bucketCount <= bucket {
		for channel := range a.channels {
			a.bucketMinimum[channel][a.bucketCount] = float32(math.Inf(1))
			a.bucketMaximum[channel][a.bucketCount] = float32(math.Inf(-1))
			a.bucketClipFirst[channel][a.bucketCount] = -1
			a.bucketClipLast[channel][a.bucketCount] = -1
		}
		a.bucketCount++
	}
	for channel, sample := range samples {
		a.bucketMinimum[channel][bucket] = min(a.bucketMinimum[channel][bucket], sample)
		a.bucketMaximum[channel][bucket] = max(a.bucketMaximum[channel][bucket], sample)
		if sample <= -1 || sample >= 1 {
			if a.bucketClipFirst[channel][bucket] < 0 {
				a.bucketClipFirst[channel][bucket] = frame
			}
			a.bucketClipLast[channel][bucket] = frame
		}
	}
}

func (a *waveformAnalysis) compact() {
	compacted := (a.bucketCount + 1) / 2
	for channel := range a.channels {
		for destination := range compacted {
			left := destination * 2
			right := min(left+1, a.bucketCount-1)
			a.bucketMinimum[channel][destination] = min(a.bucketMinimum[channel][left], a.bucketMinimum[channel][right])
			a.bucketMaximum[channel][destination] = max(a.bucketMaximum[channel][left], a.bucketMaximum[channel][right])
			first := a.bucketClipFirst[channel][left]
			if first < 0 {
				first = a.bucketClipFirst[channel][right]
			}
			a.bucketClipFirst[channel][destination] = first
			last := a.bucketClipLast[channel][right]
			if last < 0 {
				last = a.bucketClipLast[channel][left]
			}
			a.bucketClipLast[channel][destination] = last
		}
	}
	a.bucketCount = compacted
	a.bucketFrames *= 2
}

func (a *waveformAnalysis) finish(total int64) {
	total = max(total, 1)
	a.minimum = make([][]float32, a.channels)
	a.maximum = make([][]float32, a.channels)
	a.clipped = make([][]bool, a.channels)
	a.seen = make([]bool, waveformPlotWidth)
	for channel := range a.channels {
		a.minimum[channel] = make([]float32, waveformPlotWidth)
		a.maximum[channel] = make([]float32, waveformPlotWidth)
		a.clipped[channel] = make([]bool, waveformPlotWidth)
		for column := range waveformPlotWidth {
			a.minimum[channel][column] = float32(math.Inf(1))
			a.maximum[channel][column] = float32(math.Inf(-1))
		}
	}
	for bucket := range a.bucketCount {
		start := int64(bucket) * a.bucketFrames
		end := min(total, start+a.bucketFrames)
		if start >= total || end <= start {
			continue
		}
		columnStart := min(int(start*waveformPlotWidth/total), waveformPlotWidth-1)
		columnEnd := min(int((end*waveformPlotWidth+total-1)/total)-1, waveformPlotWidth-1)
		for column := columnStart; column <= columnEnd; column++ {
			a.seen[column] = true
			for channel := range a.channels {
				a.minimum[channel][column] = min(a.minimum[channel][column], a.bucketMinimum[channel][bucket])
				a.maximum[channel][column] = max(a.maximum[channel][column], a.bucketMaximum[channel][bucket])
			}
		}
		for channel := range a.channels {
			first := a.bucketClipFirst[channel][bucket]
			if first < 0 {
				continue
			}
			last := a.bucketClipLast[channel][bucket]
			// A compacted bucket spans at most half a pixel, so clipped
			// samples in it can reach only adjacent output columns.
			clipStart := min(int(first*waveformPlotWidth/total), waveformPlotWidth-1)
			clipEnd := min(int(((last+1)*waveformPlotWidth+total-1)/total)-1, waveformPlotWidth-1)
			for column := clipStart; column <= clipEnd; column++ {
				a.clipped[channel][column] = true
			}
		}
	}
	for column := range waveformPlotWidth {
		if a.seen[column] {
			continue
		}
		for channel := range a.channels {
			a.minimum[channel][column] = 0
			a.maximum[channel][column] = 0
		}
	}
	a.bucketMinimum = nil
	a.bucketMaximum = nil
	a.bucketClipFirst = nil
	a.bucketClipLast = nil
}

type spectrogramAnalysis struct {
	channels     int
	total        int64
	hop          int64
	next         int64
	blockSteps   int64
	bucketFrames int64
	compacted    bool
	window       []float64
	windowSum    float64
	edgeWindow   []float64
	plan         *fft.RealPlan
	ring         [][]float64
	bucketLimit  int
	bucketCount  int
	bucketCounts []uint32
	bucketPower  [][]float32
	power        [][]float32
	counts       []uint32
	fftInput     []float64
	fftOutput    []complex128
}

const spectrogramBatchFrames = 32 << 10

type spectrogramBatch struct {
	start   int64
	frames  int
	samples []float32
}

// parallelSpectrogram keeps every channel's FFT and bucket accumulation
// sequential, while processing independent channels in bounded PCM batches.
type parallelSpectrogram struct {
	channels int
	analyses []*spectrogramAnalysis
	buffer   []float32
	used     int
	start    int64
	jobs     []chan spectrogramBatch
	done     chan struct{}
	workers  sync.WaitGroup
}

func newParallelSpectrogram(channels int, sampleRate int, estimatedTotal int64) *parallelSpectrogram {
	workerCount := min(channels, runtime.GOMAXPROCS(0), 4)
	p := &parallelSpectrogram{
		channels: channels,
		analyses: make([]*spectrogramAnalysis, channels),
		buffer:   make([]float32, spectrogramBatchFrames*channels),
		jobs:     make([]chan spectrogramBatch, workerCount),
		done:     make(chan struct{}, workerCount),
	}
	for channel := range channels {
		p.analyses[channel] = newSpectrogramAnalysis(1, sampleRate, estimatedTotal)
	}
	for worker := range workerCount {
		jobs := make(chan spectrogramBatch)
		p.jobs[worker] = jobs
		p.workers.Go(func() {
			for batch := range jobs {
				for channel := worker; channel < channels; channel += workerCount {
					analysis := p.analyses[channel]
					for frame := range batch.frames {
						analysis.add(batch.start+int64(frame), []float32{batch.samples[frame*channels+channel]})
					}
				}
				p.done <- struct{}{}
			}
		})
	}
	return p
}

func (p *parallelSpectrogram) add(frame int64, samples []float32) {
	if p.used == 0 {
		p.start = frame
	}
	copy(p.buffer[p.used*p.channels:], samples)
	p.used++
	if p.used == spectrogramBatchFrames {
		p.flush()
	}
}

func (p *parallelSpectrogram) flush() {
	if p.used == 0 {
		return
	}
	batch := spectrogramBatch{
		start:   p.start,
		frames:  p.used,
		samples: p.buffer[:p.used*p.channels],
	}
	for _, jobs := range p.jobs {
		jobs <- batch
	}
	for range p.jobs {
		<-p.done
	}
	p.used = 0
}

func (p *parallelSpectrogram) finish(total int64) *spectrogramAnalysis {
	p.flush()
	merged := &spectrogramAnalysis{
		channels:    p.channels,
		bucketPower: make([][]float32, p.channels),
		power:       make([][]float32, p.channels),
	}
	for channel, analysis := range p.analyses {
		analysis.finish(total)
		merged.bucketPower[channel] = analysis.bucketPower[0]
		merged.power[channel] = analysis.power[0]
		if channel == 0 {
			merged.total = analysis.total
			merged.hop = analysis.hop
			merged.blockSteps = analysis.blockSteps
			merged.windowSum = analysis.windowSum
			merged.compacted = analysis.compacted
			merged.bucketCount = analysis.bucketCount
			merged.bucketCounts = analysis.bucketCounts
			merged.counts = analysis.counts
		}
	}
	return merged
}

func (p *parallelSpectrogram) close() {
	for _, jobs := range p.jobs {
		close(jobs)
	}
	p.workers.Wait()
}

func newSpectrogramAnalysis(channels int, sampleRate int, estimatedTotal int64) *spectrogramAnalysis {
	analysis := &spectrogramAnalysis{
		channels:    channels,
		window:      make([]float64, spectrogramFFTSize),
		edgeWindow:  make([]float64, spectrogramFFTSize),
		plan:        fft.NewRealPlan(spectrogramFFTSize),
		ring:        make([][]float64, channels),
		bucketPower: make([][]float32, channels),
		fftInput:    make([]float64, spectrogramFFTSize),
		fftOutput:   make([]complex128, spectrogramBins),
	}
	windowDenominator := besselI0(spectrogramKaiserBeta)
	for index := range spectrogramFFTSize {
		position := 2*float64(index)/float64(spectrogramFFTSize) - 1
		analysis.window[index] = besselI0(spectrogramKaiserBeta*math.Sqrt(1-position*position)) / windowDenominator
		analysis.windowSum += analysis.window[index]
	}
	for index := range analysis.window {
		analysis.window[index] *= 2 / analysis.windowSum
	}
	analysis.hop, analysis.blockSteps = spectrogramGeometry(sampleRate, estimatedTotal, analysis.windowSum)
	analysis.bucketFrames = analysis.hop * analysis.blockSteps
	analysis.next = analysis.hop / 2
	// Integer step rounding can produce slightly more native columns than the rendered width.
	analysis.bucketLimit = max(spectrogramPlotWidth, int((max(estimatedTotal, 1)-1)/analysis.bucketFrames+1))
	analysis.bucketCounts = make([]uint32, analysis.bucketLimit)
	for channel := range channels {
		analysis.ring[channel] = make([]float64, spectrogramFFTSize)
		analysis.bucketPower[channel] = make([]float32, analysis.bucketLimit*spectrogramBins)
	}
	return analysis
}

func spectrogramGeometry(sampleRate int, total int64, windowSum float64) (int64, int64) {
	// SoX caps pixels per second at 5000 and truncates frames per column to an integer.
	columnFrames := max(1, int64(math.Max(float64(sampleRate)/5000, float64(max(total, 1))/spectrogramPlotWidth)))
	windowsPerColumn := max(1, math.Ceil(float64(columnFrames)/math.Round(windowSum)))
	hop := max(1, int64(math.Round(float64(columnFrames)/windowsPerColumn)))
	blockSteps := max(1, int64(math.Round(float64(columnFrames)/float64(hop))))
	return hop, blockSteps
}

func (a *spectrogramAnalysis) matchesGeometry(sampleRate int, total int64) bool {
	hop, blockSteps := spectrogramGeometry(sampleRate, total, a.windowSum)
	return !a.compacted && a.hop == hop && a.blockSteps == blockSteps
}

func besselI0(value float64) float64 {
	sum, term := 1.0, 1.0
	for index := 1; ; index++ {
		term *= value * value / (4 * float64(index*index))
		sum += term
		if term <= sum*1e-15 {
			return sum
		}
	}
}

func (a *spectrogramAnalysis) add(frame int64, samples []float32) {
	for channel, sample := range samples {
		a.ring[channel][frame%spectrogramFFTSize] = float64(sample)
	}
	half := int64(spectrogramFFTSize / 2)
	for frame >= a.next+half-1 {
		a.accumulateWindow(a.next, frame+1)
		a.next += a.hop
	}
}

func (a *spectrogramAnalysis) finish(total int64) {
	a.total = max(total, 1)
	for a.next < a.total {
		a.accumulateWindow(a.next, a.total)
		a.next += a.hop
	}
	a.counts = a.bucketCounts
	a.power = a.bucketPower
}

func (a *spectrogramAnalysis) compact() {
	a.compacted = true
	compacted := (a.bucketCount + 1) / 2
	for destination := range compacted {
		left := destination * 2
		right := min(left+1, a.bucketCount-1)
		a.bucketCounts[destination] = a.bucketCounts[left]
		if right != left {
			a.bucketCounts[destination] += a.bucketCounts[right]
		}
		for channel := range a.channels {
			destinationOffset := destination * spectrogramBins
			leftOffset := left * spectrogramBins
			rightOffset := right * spectrogramBins
			for bin := range spectrogramBins {
				a.bucketPower[channel][destinationOffset+bin] = a.bucketPower[channel][leftOffset+bin]
				if right != left {
					a.bucketPower[channel][destinationOffset+bin] += a.bucketPower[channel][rightOffset+bin]
				}
			}
		}
	}
	a.bucketCount = compacted
	a.bucketFrames *= 2
}

func (a *spectrogramAnalysis) windowForRange(start int64, available int64) []float64 {
	left := int(min(max(-start, 0), spectrogramFFTSize))
	right := int(min(max(start+spectrogramFFTSize-available, 0), int64(spectrogramFFTSize-left)))
	if left == 0 && right == 0 {
		return a.window
	}
	clear(a.edgeWindow)
	span := spectrogramFFTSize - left - right
	if span == 0 {
		return a.edgeWindow
	}
	denominator := besselI0(spectrogramKaiserBeta)
	sum := 0.0
	for index := range span {
		position := 2*float64(index)/float64(span) - 1
		weight := besselI0(spectrogramKaiserBeta*math.Sqrt(1-position*position)) / denominator
		a.edgeWindow[left+index] = weight
		sum += weight
	}
	adjustment := float64(span) / spectrogramFFTSize
	scale := 2 / sum * adjustment * adjustment
	for index := range span {
		a.edgeWindow[left+index] *= scale
	}
	return a.edgeWindow
}

func (a *spectrogramAnalysis) accumulateWindow(center int64, available int64) {
	for center/a.bucketFrames >= int64(a.bucketLimit) {
		a.compact()
	}
	bucket := int(center / a.bucketFrames)
	for a.bucketCount <= bucket {
		index := a.bucketCount
		a.bucketCounts[index] = 0
		for channel := range a.channels {
			offset := index * spectrogramBins
			clear(a.bucketPower[channel][offset : offset+spectrogramBins])
		}
		a.bucketCount++
	}
	a.bucketCounts[bucket]++
	half := int64(spectrogramFFTSize / 2)
	start := center - half
	interior := start >= 0 && start+spectrogramFFTSize == available
	window := a.window
	if !interior {
		window = a.windowForRange(start, available)
	}
	first := 0
	if interior {
		first = int(start % spectrogramFFTSize)
	}
	count := spectrogramFFTSize - first
	for channel := range a.channels {
		if interior {
			for index := range count {
				a.fftInput[index] = a.ring[channel][first+index] * window[index]
			}
			for index := count; index < spectrogramFFTSize; index++ {
				a.fftInput[index] = a.ring[channel][index-count] * window[index]
			}
		} else {
			clear(a.fftInput)
			for index := range spectrogramFFTSize {
				source := start + int64(index)
				if source >= 0 && source < available && available-source <= spectrogramFFTSize {
					a.fftInput[index] = a.ring[channel][source%spectrogramFFTSize] * window[index]
				}
			}
		}
		a.plan.RFFT(a.fftOutput, a.fftInput)
		offset := bucket * spectrogramBins
		for bin, value := range a.fftOutput {
			a.bucketPower[channel][offset+bin] += float32(real(value)*real(value) + imag(value)*imag(value))
		}
	}
}

func (a *spectrogramAnalysis) decibels(channel int, column int, bin int) float64 {
	if channel < 0 || channel >= a.channels || column < 0 || column >= spectrogramPlotWidth || bin < 0 || bin >= spectrogramBins {
		return spectrogramFloorDB
	}
	if a.bucketCount == 0 {
		return spectrogramFloorDB
	}
	column = min(column*a.bucketCount/spectrogramPlotWidth, a.bucketCount-1)
	count := a.counts[column]
	if count == 0 {
		return spectrogramFloorDB
	}
	power := float64(a.power[channel][column*spectrogramBins+bin]) / float64(count)
	if power <= 0 || math.IsNaN(power) || math.IsInf(power, 0) {
		return spectrogramFloorDB
	}
	return max(spectrogramFloorDB, min(0, 10*math.Log10(power)))
}
