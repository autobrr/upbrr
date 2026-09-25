// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const pcmReadBufferSize = 64 << 10

var errMalformedPCM = errors.New("audio analysis: malformed PCM stream")

// consumePCM reads interleaved little-endian float32 sample frames without
// retaining the complete decoded stream. Frame indexes and the returned count
// are per-channel PCM sample frames, not individual float values.
func consumePCM(reader io.Reader, channels int, consume func(int64, []float32) error) (int64, error) {
	if reader == nil || channels <= 0 {
		return 0, errMalformedPCM
	}
	frameBytes := channels * 4
	buffer := make([]byte, pcmReadBufferSize+frameBytes)
	values := make([]float32, channels)
	kept := 0
	var frames int64
	for {
		read, readErr := reader.Read(buffer[kept:pcmReadBufferSize])
		available := kept + read
		complete := available - available%frameBytes
		for offset := 0; offset < complete; offset += frameBytes {
			for channel := range channels {
				bits := binary.LittleEndian.Uint32(buffer[offset+channel*4:])
				value := math.Float32frombits(bits)
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return frames, fmt.Errorf("%w: non-finite sample at frame %d channel %d", errMalformedPCM, frames, channel+1)
				}
				values[channel] = value
			}
			if consume != nil {
				if err := consume(frames, values); err != nil {
					return frames, err
				}
			}
			frames++
		}
		kept = copy(buffer, buffer[complete:available])
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if kept != 0 {
					return frames, fmt.Errorf("%w: truncated sample frame", errMalformedPCM)
				}
				return frames, nil
			}
			return frames, fmt.Errorf("audio analysis: read PCM stream: %w", readErr)
		}
		if read == 0 {
			return frames, io.ErrNoProgress
		}
	}
}
