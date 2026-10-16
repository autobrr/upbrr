// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"errors"
	"math"
)

// SharedBatchMemoryBytes includes the active DSP group, one corrective DSP
// overlap, image/PNG scratch, a retained HDR track report and decoder buffers.
// The HDR caller must charge this reservation alongside native/picture evidence.
const SharedBatchMemoryBytes int64 = 768 << 20

// ErrBatchMemory permits the caller to reduce the next batch before decoding.
var ErrBatchMemory = errors.New("prepared audio batch exceeds its memory reservation")

func batchMemory(bindings []trackBinding) error {
	// Two retained PNGs (64 MiB), encoded report (96 MiB), and bounded
	// FFmpeg/transport/runtime scratch (64 MiB).
	used := int64(224 << 20)
	var largestDSP, largestRender int64
	for _, binding := range bindings {
		frames, err := boundedAudioFrames(binding)
		if err != nil {
			return err
		}
		dsp, render := audioTrackMemory(binding.track.Channels, binding.track.SampleRate, frames)
		used += dsp
		largestDSP, largestRender = max(largestDSP, dsp), max(largestRender, render)
	}
	if used+largestDSP+largestRender > SharedBatchMemoryBytes {
		return ErrBatchMemory
	}
	return nil
}

func batchCorrectionMemory(analyses []*trackAnalysis, failures map[int]error, corrected *trackAnalysis) error {
	used := int64(224 << 20)
	var largestRender int64
	for i, analysis := range analyses {
		if analysis == nil || failures[i] != nil {
			continue
		}
		frames, err := boundedAudioFrames(analysis.binding)
		if err != nil {
			return err
		}
		dsp, render := audioTrackMemory(analysis.binding.track.Channels, analysis.binding.track.SampleRate, frames)
		used += dsp
		largestRender = max(largestRender, render)
	}
	// The original DSP is still live while verified correction runs. Reject the
	// changed geometry before constructing or launching that additional analyzer.
	binding := corrected.binding
	binding.durationSeconds = float64(corrected.frames) / float64(binding.track.SampleRate)
	if _, err := boundedAudioFrames(binding); err != nil {
		return err
	}
	dsp, render := audioTrackMemory(binding.track.Channels, binding.track.SampleRate, corrected.frames)
	if used+dsp+max(largestRender, render) > SharedBatchMemoryBytes {
		return ErrBatchMemory
	}
	return nil
}

func boundedAudioFrames(binding trackBinding) (int64, error) {
	if binding.track.Channels < 1 || binding.track.Channels > 8 || binding.track.SampleRate < 1 || binding.track.SampleRate > 768000 ||
		math.IsNaN(binding.durationSeconds) || math.IsInf(binding.durationSeconds, 0) || binding.durationSeconds < 0 || binding.durationSeconds > 24*60*60 {
		return 0, errors.New("shared audio has unsupported channel, sample-rate or duration geometry")
	}
	frames := int64(math.Ceil(binding.durationSeconds * float64(binding.track.SampleRate)))
	if frames == 0 {
		frames = int64(binding.track.SampleRate) * 60
	}
	return frames, nil
}

func audioTrackMemory(channels, rate int, frames int64) (int64, int64) {
	// Use the same Kaiser sum and integer geometry as the DSP, without allocating
	// its per-channel arrays. All inputs are bounded before this arithmetic.
	denominator, sum := besselI0(spectrogramKaiserBeta), 0.0
	for index := range spectrogramFFTSize {
		position := 2*float64(index)/float64(spectrogramFFTSize) - 1
		sum += besselI0(spectrogramKaiserBeta*math.Sqrt(1-position*position)) / denominator
	}
	hop, steps := spectrogramGeometry(rate, frames, sum)
	columns := max(int64(spectrogramPlotWidth), (max(frames, 1)-1)/(hop*steps)+1)
	c := int64(channels)
	// Both accumulation and finalized powers/counts can coexist. Each parallel
	// channel owns its FFT/window/ring; PCM transport and wave output stay bounded.
	dsp := c*(columns*(spectrogramBins*8+8)+waveformBucketCapacity*24+waveformPlotWidth*9+spectrogramBatchFrames*4+256<<10) + 1<<20
	width := int64(spectrogramLeftMargin + spectrogramPlotWidth + spectrogramAxisGap + spectrogramLegendWidth + spectrogramRightMargin)
	height := c*(spectrogramPanelHeight+1) + spectrogramTopMargin + spectrogramBottomMargin
	// One rendered RGBA image, PNG encoder/filter scratch and encoded image copy.
	render := width*height*12 + 8<<20
	return dsp, render
}
