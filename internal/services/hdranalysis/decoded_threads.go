// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import "runtime"

// requestedVideoThreads applies the existing 512 MiB RGB-sized in-flight
// picture allowance to an explicit thread request. Zero preserves each legacy
// mode's original default. Zero for an explicit request means the simultaneous
// inputs cannot fit even one decoder thread each. Sparse decoding has its separate SPS-based budget.
func requestedVideoThreads(requested, fallback int, inputs []DecodedInput) int {
	if requested == 0 {
		return fallback
	}
	var frameBytes int64
	for _, input := range inputs {
		if input.Source.Width < 1 || input.Source.Width > 8192 || input.Source.Height < 1 || input.Source.Height > 4320 {
			return 1
		}
		frameBytes += int64(input.Source.Width) * int64(input.Source.Height) * 6
	}
	return min(max(1, requested), 64, runtime.GOMAXPROCS(0)/max(1, len(inputs)), int(max(1, (512<<20)/max(1, frameBytes))))
}
