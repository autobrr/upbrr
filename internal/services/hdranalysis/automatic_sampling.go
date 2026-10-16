// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"slices"
	"strconv"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (p *automaticPictures) consume(ctx context.Context, reader io.Reader, first, step int, progress func(int)) error {
	p.sampleFirst, p.sampleStep = first, step
	frame := make([]byte, p.width*p.height*6)
	lastProgress := time.Time{}
	for {
		n, err := io.ReadFull(reader, frame)
		if n == 0 && errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read automatic picture: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("measure automatic picture: %w", err)
		}
		if err := p.observeAt(frame, first+len(p.frames)*step); err != nil {
			return err
		}
		if progress != nil && time.Since(lastProgress) >= 2*time.Second {
			lastProgress = time.Now()
			progress(len(p.frames))
		}
	}
}

func automaticSampleStep(source api.HDRDecodedSourceEvidence) int {
	numerator, ne := strconv.ParseFloat(source.FrameRateNumerator, 64)
	denominator, de := strconv.ParseFloat(source.FrameRateDenominator, 64)
	if ne == nil && de == nil && finiteWithin(numerator/denominator, 1, 240) {
		return int(math.Ceil(numerator / denominator))
	}
	// shortcut: unknown header cadence uses 24-frame spacing; exact retained
	// timestamps and frame spacing label coverage without claiming one second.
	return 24
}

func automaticFirstSample(offset, step int) int {
	return (offset%step + step) % step
}

func automaticSampleCount(total, first, step int) int {
	if first >= total {
		return 0
	}
	return 1 + (total-1-first)/step
}

func automaticFrameTotal(report *MeasurementReport, reference bool) int {
	if sampling := report.Automatic.Sampling; sampling != nil {
		if reference {
			return sampling.Frames[1]
		}
		return sampling.Frames[0]
	}
	if reference {
		return len(report.Automatic.Reference)
	}
	return len(report.Automatic.Frames)
}

func automaticPreviewFilter(source api.HDRDecodedSourceEvidence, profile5 bool) string {
	if profile5 {
		return automaticColourFilter(source, true) + fmt.Sprintf(":w=%d:h=%d", sceneWidth, sceneHeight)
	}
	return fmt.Sprintf("scale=%d:%d:flags=neighbor,setsar=1,", sceneWidth, sceneHeight) + automaticColourFilter(source, false)
}

func automaticFrameNumber(report *MeasurementReport, index int, reference bool) int {
	frames := report.Automatic.Frames
	if reference {
		frames = report.Automatic.Reference
	}
	if report.Automatic.Sampling != nil {
		return frames[index].Index
	}
	return index
}

func automaticFrameAt(report *MeasurementReport, number int, reference bool) (AutomaticFrame, bool) {
	frames := report.Automatic.Frames
	if reference {
		frames = report.Automatic.Reference
	}
	if report.Automatic.Sampling == nil {
		if number < 0 || number >= len(frames) {
			return AutomaticFrame{}, false
		}
		return frames[number], true
	}
	index, ok := slices.BinarySearchFunc(frames, number, func(frame AutomaticFrame, number int) int {
		return frame.Index - number
	})
	if !ok {
		return AutomaticFrame{}, false
	}
	return frames[index], true
}

func setAutomaticSampleCadence(report *MeasurementReport, proofs []*automaticProof) error {
	if report.Automatic.Sampling == nil || len(report.Automatic.Reference) == 0 {
		return nil
	}
	left, err := exactCadence(proofs[0].times)
	if err != nil {
		return err
	}
	right, err := exactCadence(proofs[1].times)
	if err != nil || left.Cmp(right) != 0 {
		return errDecodedMapping
	}
	if !left.Num().IsInt64() || !left.Denom().IsUint64() {
		return errDecodedMapping
	}
	stamp := measurementTimestamp(pictureTime{
		Value:     left.Num().Int64(),
		Timescale: left.Denom().Uint64(),
		Valid:     true,
	})
	report.Automatic.Sampling.Cadence = &stamp
	return nil
}

func validateAutomaticSampleComparison(report *MeasurementReport) error {
	sampling := report.Automatic.Sampling
	if sampling.Cadence == nil || sampling.Frames[1] < 2 || len(report.Automatic.Reference) == 0 ||
		report.Automatic.Reference[0].Index != automaticFirstSample(*report.Options.Comparison.FrameOffset, sampling.Step) {
		return errDecodedMapping
	}
	cadence, err := automaticTime(*sampling.Cadence)
	if err != nil || cadence.rational().Sign() <= 0 {
		return errDecodedMapping
	}
	for source, frames := range [][]AutomaticFrame{report.Automatic.Frames, report.Automatic.Reference} {
		origin, err := automaticTime(sampling.Origins[source])
		if err != nil {
			return err
		}
		for _, frame := range frames {
			stamp, err := automaticTime(frame.PTS)
			if err != nil {
				return err
			}
			expected := new(big.Rat).Add(origin.rational(), new(big.Rat).Mul(cadence.rational(), big.NewRat(int64(frame.Index), 1)))
			if stamp.rational().Cmp(expected) != 0 {
				return errDecodedMapping
			}
		}
	}
	paired := 0
	for _, frame := range report.Automatic.Frames {
		if _, exists := automaticFrameAt(report, frame.Index+*report.Options.Comparison.FrameOffset, true); exists {
			paired++
		}
	}
	if paired < 2 {
		return errDecodedMapping
	}
	return nil
}
