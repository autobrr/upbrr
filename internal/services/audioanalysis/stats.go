// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	statsWindowSeconds = 0.05
	soxSampleScale     = float64(1 << 31)
)

// statsAnalysis is an independent Go implementation of the measurements used
// by libSoX 14.4.2's stats effect (LGPL-2.1-or-later). It preserves its
// per-channel extrema, exponential RMS window, peak-run, and bit-depth
// semantics while operating on the existing decoded PCM frame loop.
type statsAnalysis struct {
	channels      []statsChannel
	multiplier    float64
	warmupSamples int64
	sampleRate    int
	finished      bool
}

type statsChannel struct {
	last              float64
	sum               float64
	sumSquares        float64
	averageSquares    float64
	minAverageSquares float64
	maxAverageSquares float64
	min               float64
	max               float64
	minRun            float64
	minRuns           float64
	maxRun            float64
	maxRuns           float64
	samples           int64
	minCount          int64
	maxCount          int64
	mask              uint32
}

func newStatsAnalysis(channels int, sampleRate int) *statsAnalysis {
	analysis := &statsAnalysis{
		channels:      make([]statsChannel, channels),
		sampleRate:    sampleRate,
		multiplier:    math.Exp((-1 / statsWindowSeconds) / float64(sampleRate)),
		warmupSamples: int64(5*statsWindowSeconds*float64(sampleRate) + 0.5),
	}
	for channel := range analysis.channels {
		analysis.channels[channel].min = 2
		analysis.channels[channel].max = -2
		analysis.channels[channel].minAverageSquares = 2
	}
	return analysis
}

func (a *statsAnalysis) add(samples []float32) {
	for channel, sample := range samples {
		quantized := soxSampleFromFloat32(sample)
		// SoX stats receives signed 32-bit PCM after float decoding. Keep this
		// quantization local to numeric reporting; image analyzers receive the
		// original decoded float32 samples unchanged.
		//nolint:gosec // Reinterpret signed PCM as its two's-complement bit pattern for the bit-depth mask.
		a.channels[channel].add(float64(quantized)/soxSampleScale, uint32(quantized), a.multiplier, a.warmupSamples)
	}
}

func (s *statsChannel) add(sample float64, quantized uint32, multiplier float64, warmupSamples int64) {
	switch {
	case sample < s.min:
		s.min, s.minCount, s.minRun, s.minRuns = sample, 1, 1, 0
	case sample == s.min:
		s.minCount++
		if sample == s.last {
			s.minRun++
		} else {
			s.minRun = 1
		}
	case s.last == s.min:
		s.minRuns += s.minRun * s.minRun
	}
	switch {
	case sample > s.max:
		s.max, s.maxCount, s.maxRun, s.maxRuns = sample, 1, 1, 0
	case sample == s.max:
		s.maxCount++
		if sample == s.last {
			s.maxRun++
		} else {
			s.maxRun = 1
		}
	case s.last == s.max:
		s.maxRuns += s.maxRun * s.maxRun
	}

	square := sample * sample
	s.sum += sample
	s.sumSquares += square
	s.averageSquares = s.averageSquares*multiplier + (1-multiplier)*square
	// The C implementation has no populated window at exactly the warm-up
	// boundary. Treat that boundary as a short track and use its full RMS.
	if s.samples >= warmupSamples {
		s.maxAverageSquares = max(s.maxAverageSquares, s.averageSquares)
		s.minAverageSquares = min(s.minAverageSquares, s.averageSquares)
	}
	s.last = sample
	s.mask |= quantized
	s.samples++
}

func (a *statsAnalysis) report() string {
	if !a.finished {
		for channel := range a.channels {
			a.channels[channel].finish(a.warmupSamples)
		}
		a.finished = true
	}
	return formatStatsReport(a.channels, a.sampleRate)
}

func (s *statsChannel) finish(warmupSamples int64) {
	if s.samples == 0 {
		return
	}
	if s.last == s.min {
		s.minRuns += s.minRun * s.minRun
	}
	if s.last == s.max {
		s.maxRuns += s.maxRun * s.maxRun
	}
	if s.samples <= warmupSamples {
		s.minAverageSquares = s.sumSquares / float64(s.samples)
		s.maxAverageSquares = s.minAverageSquares
	}
}

func formatStatsReport(channels []statsChannel, sampleRate int) string {
	if len(channels) == 0 || sampleRate <= 0 || channels[0].samples == 0 {
		return ""
	}
	var report strings.Builder
	if len(channels) == 2 {
		report.WriteString("             Overall     Left      Right\n")
	} else if len(channels) > 2 {
		report.WriteString("             Overall")
		for channel := range channels {
			fmt.Fprintf(&report, "     Ch%-3d", channel+1)
		}
		report.WriteByte('\n')
	}

	overall := aggregateStats(channels)
	writeStatsFloatRow(&report, "DC offset ", overall.dcOffset, channels, func(channel statsChannel) float64 {
		return channel.sum / float64(channel.samples)
	})
	writeStatsFloatRow(&report, "Min level ", overall.min, channels, func(channel statsChannel) float64 { return channel.min })
	writeStatsFloatRow(&report, "Max level ", overall.max, channels, func(channel statsChannel) float64 { return channel.max })
	writeStatsDBRow(&report, "Pk lev dB ", overall.peak, channels, func(channel statsChannel) float64 {
		return max(-channel.min, channel.max)
	})
	writeStatsDBRow(&report, "RMS lev dB", overall.rms, channels, func(channel statsChannel) float64 {
		return math.Sqrt(channel.sumSquares / float64(channel.samples))
	})
	writeStatsDBRow(&report, "RMS Pk dB ", overall.rmsPeak, channels, func(channel statsChannel) float64 {
		return math.Sqrt(channel.maxAverageSquares)
	})
	report.WriteString("RMS Tr dB ")
	writeStatsRMSRange(&report, overall.rmsTrough)
	if len(channels) > 1 {
		for _, channel := range channels {
			writeStatsRMSRange(&report, channel.minAverageSquares)
		}
	}
	report.WriteByte('\n')

	report.WriteString("Crest factor")
	if len(channels) > 1 {
		report.WriteString("       -")
	} else {
		fmt.Fprintf(&report, " %7.2f", overall.crestFactor)
	}
	if len(channels) > 1 {
		for _, channel := range channels {
			fmt.Fprintf(&report, "%10.2f", crestFactor(channel))
		}
	}
	report.WriteByte('\n')

	fmt.Fprintf(&report, "Flat factor%s", formatStatsDB(linearToDB(overall.flatFactor), 9))
	if len(channels) > 1 {
		for _, channel := range channels {
			fmt.Fprintf(&report, " %s", formatStatsDB(linearToDB(flatFactor(channel)), 9))
		}
	}
	report.WriteByte('\n')

	fmt.Fprintf(&report, "Pk count   %9s", formatStatsCount(overall.peakCount))
	if len(channels) > 1 {
		for _, channel := range channels {
			fmt.Fprintf(&report, " %9s", formatStatsCount(float64(channel.minCount+channel.maxCount)))
		}
	}
	report.WriteByte('\n')

	overallDepth, overallSourceDepth := statsBitDepth(overall.mask, overall.min, overall.max)
	fmt.Fprintf(&report, "Bit-depth      %2d/%-2d", overallDepth, overallSourceDepth)
	if len(channels) > 1 {
		for _, channel := range channels {
			depth, sourceDepth := statsBitDepth(channel.mask, channel.min, channel.max)
			fmt.Fprintf(&report, "     %2d/%-2d", depth, sourceDepth)
		}
	}
	fmt.Fprintf(&report, "\nNum samples%9s", formatStatsCount(float64(channels[0].samples)))
	fmt.Fprintf(&report, "\nLength s   %9.3f", float64(channels[0].samples)/float64(sampleRate))
	report.WriteString("\nScale max   1.000000")
	fmt.Fprintf(&report, "\nWindow s   %9.3f\n", statsWindowSeconds)
	return report.String()
}

type aggregateStatsResult struct {
	dcOffset    float64
	min         float64
	max         float64
	peak        float64
	rms         float64
	rmsPeak     float64
	rmsTrough   float64
	crestFactor float64
	flatFactor  float64
	peakCount   float64
	mask        uint32
}

func aggregateStats(channels []statsChannel) aggregateStatsResult {
	result := aggregateStatsResult{
		min:       2,
		max:       -2,
		rmsTrough: 2,
	}
	var maximumSum float64
	var averagePeak float64
	var totalSamples int64
	var sumSquares float64
	var minRuns float64
	var maxRuns float64
	var minCount int64
	var maxCount int64
	for _, channel := range channels {
		result.min = min(result.min, channel.min)
		result.max = max(result.max, channel.max)
		result.rmsPeak = max(result.rmsPeak, channel.maxAverageSquares)
		result.rmsTrough = min(result.rmsTrough, channel.minAverageSquares)
		if math.Abs(channel.sum) > math.Abs(maximumSum) {
			maximumSum = channel.sum
		}
		totalSamples += channel.samples
		sumSquares += channel.sumSquares
		result.mask |= channel.mask
		minRuns += channel.minRuns
		maxRuns += channel.maxRuns
		minCount += channel.minCount
		maxCount += channel.maxCount
		averagePeak += max(-channel.min, channel.max)
	}
	result.dcOffset = maximumSum / float64(channels[0].samples)
	result.peak = max(-result.min, result.max)
	result.rms = math.Sqrt(sumSquares / float64(totalSamples))
	result.rmsPeak = math.Sqrt(result.rmsPeak)
	result.crestFactor = averagePeak / float64(len(channels)) / result.rms
	if sumSquares == 0 {
		result.crestFactor = 1
	}
	result.flatFactor = (minRuns + maxRuns) / float64(minCount+maxCount)
	result.peakCount = float64(minCount+maxCount) / float64(len(channels))
	return result
}

func writeStatsFloatRow(
	report *strings.Builder,
	label string,
	overall float64,
	channels []statsChannel,
	value func(statsChannel) float64,
) {
	fmt.Fprintf(report, "%s %9.6f", label, overall)
	if len(channels) > 1 {
		for _, channel := range channels {
			fmt.Fprintf(report, " %9.6f", value(channel))
		}
	}
	report.WriteByte('\n')
}

func writeStatsDBRow(
	report *strings.Builder,
	label string,
	overall float64,
	channels []statsChannel,
	value func(statsChannel) float64,
) {
	fmt.Fprintf(report, "%s%s", label, formatStatsDB(linearToDB(overall), 10))
	if len(channels) > 1 {
		for _, channel := range channels {
			fmt.Fprintf(report, "%s", formatStatsDB(linearToDB(value(channel)), 10))
		}
	}
	report.WriteByte('\n')
}

func writeStatsRMSRange(report *strings.Builder, averageSquares float64) {
	if averageSquares == 1 {
		report.WriteString("         -")
		return
	}
	report.WriteString(formatStatsDB(linearToDB(math.Sqrt(averageSquares)), 10))
}

func crestFactor(channel statsChannel) float64 {
	if channel.sumSquares == 0 {
		return 1
	}
	return max(-channel.min, channel.max) / math.Sqrt(channel.sumSquares/float64(channel.samples))
}

func flatFactor(channel statsChannel) float64 {
	return (channel.minRuns + channel.maxRuns) / float64(channel.minCount+channel.maxCount)
}

func linearToDB(value float64) float64 {
	if value == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(value)
}

func formatStatsDB(value float64, width int) string {
	if math.IsInf(value, -1) {
		return fmt.Sprintf("%*s", width, "-inf")
	}
	return fmt.Sprintf("%*.2f", width, value)
}

func formatStatsCount(value float64) string {
	if value == 0 {
		return "0"
	}
	exponent := int(math.Floor(math.Log10(value)))
	precision := math.Pow10(2 - exponent)
	rounded := math.Floor(value*precision+0.5) / precision
	if rounded >= math.Pow10(exponent+1) {
		exponent++
		precision = math.Pow10(2 - exponent)
		rounded = math.Floor(value*precision+0.5) / precision
	}
	if exponent < 3 {
		if math.Trunc(rounded) == rounded {
			return strconv.Itoa(int(rounded))
		}
		decimalPlaces := max(0, 2-exponent)
		return fmt.Sprintf("%.*f", decimalPlaces, rounded)
	}
	suffixes := "\x00kMGTPEZY"
	suffixIndex := exponent / 3
	if suffixIndex >= len(suffixes) {
		return fmt.Sprintf("%#.3g", value)
	}
	decimalPlaces := 2 - exponent%3
	return fmt.Sprintf("%.*f%c", decimalPlaces, rounded/math.Pow10(suffixIndex*3), suffixes[suffixIndex])
}

func statsBitDepth(mask uint32, minimum float64, maximum float64) (int, int) {
	result := 32
	for result > 0 && mask&1 == 0 {
		result--
		mask >>= 1
	}
	sourceDepth := result
	//nolint:gosec // Reinterpret signed PCM as its two's-complement bit pattern for the bit-depth mask.
	peakMask := uint32(soxSampleFromFloat64(maximum))
	if minimum < 0 {
		//nolint:gosec // The complement and shift require the two's-complement bits of the negative minimum.
		peakMask |= ^(uint32(soxSampleFromFloat64(minimum)) << 1)
	}
	for result > 0 && peakMask&uint32(1<<31) == 0 {
		result--
		peakMask <<= 1
	}
	return result, sourceDepth
}

func soxSampleFromFloat32(value float32) int32 {
	scaled := float64(value) * soxSampleScale
	if scaled < math.MinInt32 {
		return math.MinInt32
	}
	if scaled >= soxSampleScale {
		return math.MaxInt32
	}
	// SOX_FLOAT_32BIT_TO_SAMPLE casts after scaling, which truncates toward
	// zero. The separate float64 conversion below rounds because stats.c uses
	// SOX_FLOAT_64BIT_TO_SAMPLE only for its peak bit-depth calculation.
	return int32(scaled)
}

func soxSampleFromFloat64(value float64) int32 {
	scaled := value * soxSampleScale
	if scaled <= math.MinInt32 {
		return math.MinInt32
	}
	if scaled >= math.MaxInt32 {
		return math.MaxInt32
	}
	if scaled < 0 {
		return int32(scaled - 0.5)
	}
	return int32(scaled + 0.5)
}
