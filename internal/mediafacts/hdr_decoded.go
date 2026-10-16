// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func decodedSourceFromMediaInfo(track map[string]any, videos int) *api.HDRDecodedSourceEvidence {
	integer := func(keys ...string) int {
		value, err := strconv.Atoi(mediaInfoValue(track, keys...))
		if err != nil || value < 0 || value > 65536 {
			return 0
		}
		return value
	}
	e := &api.HDRDecodedSourceEvidence{
		DolbyVisionProfile: hdrFromMediaInfoTrack(track).DolbyVisionProfile,
		VideoStreams:       videos,
		Codec:              "hevc",
		Width:              integer("Stored_Width", "Width"),
		Height:             integer("Stored_Height", "Height"),
		BitDepth:           integer("BitDepth"),
	}
	aspect := mediaInfoValue(track, "PixelAspectRatio")
	if len(aspect) > 64 {
		aspect = ""
	}
	if value, ok := new(big.Rat).SetString(aspect); ok && value.Sign() > 0 &&
		value.Num().IsInt64() && value.Denom().IsInt64() && value.Num().Int64() <= 65536 && value.Denom().Int64() <= 65536 {
		e.PixelAspectNumerator, e.PixelAspectDenominator = int(value.Num().Int64()), int(value.Denom().Int64())
	}
	switch strings.ToLower(mediaInfoValue(track, "colour_range")) {
	case "limited":
		e.Range = "limited"
	case "full":
		e.Range = "full"
	}
	if strings.EqualFold(mediaInfoValue(track, "colour_primaries"), "BT.2020") {
		e.Primaries = "bt2020"
	}
	switch strings.ToLower(mediaInfoValue(track, "transfer_characteristics")) {
	case "pq", "smpte st 2084":
		e.Transfer = "smpte2084"
	}
	switch strings.ToLower(mediaInfoValue(track, "matrix_coefficients")) {
	case "bt.2020 non-constant", "bt.2020 non-constant luminance":
		e.Matrix = "bt2020nc"
	}
	// MediaInfo's rounded FrameRate string is deliberately not exact cadence.
	numerator := mediaInfoValue(track, "FrameRate_Num")
	denominator := mediaInfoValue(track, "FrameRate_Den")
	n, nerr := strconv.ParseUint(numerator, 10, 64)
	d, derr := strconv.ParseUint(denominator, 10, 64)
	if nerr == nil && derr == nil && n > 0 && d > 0 {
		e.FrameRateNumerator, e.FrameRateDenominator = numerator, denominator
	}
	return e
}
