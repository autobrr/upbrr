// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"errors"
	"math"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSharedBatchBudgetIncludesCorrectionAndRender(t *testing.T) {
	binding := trackBinding{track: api.MediaTrackFacts{Channels: 8, SampleRate: 48000}, durationSeconds: 3201.5}
	if err := batchMemory([]trackBinding{binding}); err != nil {
		t.Fatalf("representative eight-channel track: %v", err)
	}
	if err := batchMemory([]trackBinding{binding, binding, binding, binding}); !errors.Is(err, ErrBatchMemory) {
		t.Fatalf("four large tracks must reduce the batch before allocation: %v", err)
	}
	binding.track.Channels = 2
	if err := batchMemory([]trackBinding{binding, binding, binding, binding}); err != nil {
		t.Fatalf("four stereo tracks: %v", err)
	}
	for _, duration := range []float64{-1, math.NaN(), math.Inf(1), 86401} {
		binding.durationSeconds = duration
		if err := batchMemory([]trackBinding{binding}); err == nil {
			t.Fatalf("invalid duration accepted: %v", duration)
		}
	}
	analysis := newTrackAnalysis(trackBinding{track: api.MediaTrackFacts{Channels: 1, SampleRate: 48000}}, nil)
	analysis.frames = 86401 * 48000
	if err := batchCorrectionMemory([]*trackAnalysis{analysis}, nil, analysis); err == nil {
		t.Fatal("over-limit measured duration acquired corrective allocation authority")
	}
}
