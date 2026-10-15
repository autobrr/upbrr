// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"errors"
	"math/big"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

var errDecodedMapping = errors.New("decoded HDR mapping is unsupported")

// DecodedInput contains only privately resolved source authority. Paths never
// enter measurement reports; callers revalidate the source before publication.
type DecodedInput struct {
	Path        string
	Fingerprint string
	Source      api.HDRDecodedSourceEvidence
	Native      *NativeEvidence
}

type pictureTime struct {
	Value     int64
	Timescale uint64
	Valid     bool
}

func (t pictureTime) rational() *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(t.Value), new(big.Int).SetUint64(t.Timescale))
}

func decodedTimeline(input DecodedInput) ([]pictureTime, error) {
	s := input.Source
	if input.Path == "" || input.Native == nil || len(input.Native.Timeline) != 0 || s.VideoStreams != 1 || s.Codec != "hevc" ||
		s.Width < 2 || s.Width > 8192 || s.Height < 2 || s.Height > 4320 || s.BitDepth < 10 || s.BitDepth > 16 ||
		s.PixelAspectNumerator != 1 || s.PixelAspectDenominator != 1 || s.Range != "limited" && s.Range != "full" ||
		s.Matrix != "bt2020nc" || s.Transfer != "smpte2084" || s.Primaries != "bt2020" {
		return nil, errDecodedMapping
	}
	var times []pictureTime
	if a := input.Native.DolbyVision; a != nil {
		if a.Timeline != dovi.PresentationFrames || len(a.Pictures) > MaxFrames {
			return nil, errDecodedMapping
		}
		for _, record := range a.Records {
			if record.Profile == 5 {
				return nil, errDecodedMapping
			}
		}
		times = make([]pictureTime, len(a.Pictures))
		for i, p := range a.Pictures {
			times[i] = pictureTime{
				Value:     p.PTS.Value,
				Timescale: p.PTS.Timescale,
				Valid:     p.PTS.Valid,
			}
		}
	} else if a := input.Native.HDR10Plus; a != nil {
		if len(a.Frames) > MaxFrames {
			return nil, errDecodedMapping
		}
		times = make([]pictureTime, len(a.Frames))
		for i, p := range a.Frames {
			times[i] = pictureTime{
				Value:     p.PTS.Value,
				Timescale: p.PTS.Timescale,
				Valid:     p.PTS.Valid,
			}
		}
	}
	if len(times) == 0 {
		return nil, errDecodedMapping
	}
	for i, stamp := range times {
		if input.Source.RawHEVC {
			if stamp.Valid {
				return nil, errDecodedMapping
			}
			continue
		}
		if !stamp.Valid || stamp.Timescale == 0 || i > 0 && stamp.rational().Cmp(times[i-1].rational()) <= 0 {
			return nil, errDecodedMapping
		}
	}
	return times, nil
}

// shortcut: cross-source alignment requires exact CFR timestamps. Container-rounded
// fractional CFR remains unsupported until source tick precision is retained as
// authority; a timestamp denominator alone does not prove a rounding tolerance.
func exactCadence(times []pictureTime) (*big.Rat, error) {
	if len(times) < 2 || !times[0].Valid || times[0].Timescale == 0 {
		return nil, errDecodedMapping
	}
	var cadence *big.Rat
	for i := 1; i < len(times); i++ {
		if !times[i].Valid || times[i].Timescale == 0 {
			return nil, errDecodedMapping
		}
		delta := new(big.Rat).Sub(times[i].rational(), times[i-1].rational())
		if delta.Sign() <= 0 || cadence != nil && delta.Cmp(cadence) != 0 {
			return nil, errDecodedMapping
		}
		cadence = delta
	}
	return cadence, nil
}

func comparisonIndices(a, b []pictureTime, offset int) ([]int, []int, error) {
	ca, err := exactCadence(a)
	if err != nil {
		return nil, nil, err
	}
	cb, err := exactCadence(b)
	if err != nil || ca.Cmp(cb) != 0 || offset < -MaxFrames || offset > MaxFrames {
		return nil, nil, errDecodedMapping
	}
	first, end := max(0, -offset), min(len(a), len(b)-offset)
	if first >= end {
		return nil, nil, errDecodedMapping
	}
	count := min(120, end-first)
	left, right := make([]int, count), make([]int, count)
	for i := range count {
		index := first
		if count > 1 {
			index += i * (end - first - 1) / (count - 1)
		}
		left[i], right[i] = index, index+offset
	}
	return left, right, nil
}
