// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestConsumePCMCountsSampleFramesPerChannel(t *testing.T) {
	data := encodePCM([][]float32{{1, -1}, {0.5, -0.5}, {0.25, -0.25}})
	var frames [][]float32
	count, err := consumePCM(bytes.NewReader(data), 2, func(_ int64, values []float32) error {
		frames = append(frames, append([]float32(nil), values...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || len(frames) != 3 || frames[2][1] != -0.25 {
		t.Fatalf("count=%d frames=%v", count, frames)
	}
}

func TestConsumePCMRejectsTruncationAndNonFiniteSamples(t *testing.T) {
	truncated := append(encodePCM([][]float32{{1, 2}}), 1)
	if _, err := consumePCM(bytes.NewReader(truncated), 2, nil); !errors.Is(err, errMalformedPCM) {
		t.Fatalf("truncated error = %v", err)
	}
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if _, err := consumePCM(bytes.NewReader(encodePCM([][]float32{{value}})), 1, nil); !errors.Is(err, errMalformedPCM) {
			t.Fatalf("value=%v error=%v", value, err)
		}
	}
}

func encodePCM(frames [][]float32) []byte {
	var buffer bytes.Buffer
	for _, frame := range frames {
		for _, value := range frame {
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], math.Float32bits(value))
			buffer.Write(encoded[:])
		}
	}
	return buffer.Bytes()
}
