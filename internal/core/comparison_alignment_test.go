// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"
	"time"
)

func TestComparisonSampleChunksPreserveUsefulTiming(t *testing.T) {
	type observation struct {
		seconds float64
		elapsed time.Duration
	}
	for _, test := range []struct {
		name         string
		observations []observation
		nextWindow   float64
		wantCalls    int
	}{
		{
			name:         "short tail does not split the next section",
			observations: []observation{{8, 6 * time.Second}, {.763, 996 * time.Millisecond}},
			nextWindow:   13.507,
			wantCalls:    1,
		},
		{
			name:         "repeated clipped tails retain the learned batch",
			observations: []observation{{8, 6 * time.Second}, {.763, 996 * time.Millisecond}, {1.011, 1493 * time.Millisecond}},
			nextWindow:   13.967,
			wantCalls:    1,
		},
		{
			name:         "fast full chunk allows a larger batch",
			observations: []observation{{8, 2 * time.Second}},
			nextWindow:   32,
			wantCalls:    1,
		},
		{
			name:         "slow full chunk reduces the batch",
			observations: []observation{{8, 2 * time.Second}, {32, 40 * time.Second}},
			nextWindow:   16,
			wantCalls:    2,
		},
		{
			name:         "expensive short decode still reduces the batch",
			observations: []observation{{8, 2 * time.Second}, {2, 9 * time.Second}},
			nextWindow:   16,
			wantCalls:    2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk := 8.0
			for _, sample := range test.observations {
				chunk = comparisonSampleChunkSeconds(chunk, sample.seconds, sample.elapsed)
			}
			calls := 0
			for remaining := test.nextWindow; remaining > 1e-9; remaining -= min(chunk, remaining) {
				calls++
			}
			if calls != test.wantCalls {
				t.Fatalf("next %.3fs section needs %d decoder calls, want %d (chunk %.3fs)", test.nextWindow, calls, test.wantCalls, chunk)
			}
		})
	}
}
