// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"errors"
	"math"
	"runtime"
)

// The decoder memory bound is calibrated against this build and architecture.
// Recalibrate allocation geometry before admitting another implementation.
const (
	boundaryDecoderBuild       = "a5923073bf"
	boundaryDecoderArch        = "--arch=x86_64"
	BoundaryMeasurementProfile = "hdr-boundary-pq16-colour-v3"
	BoundaryAreaProfile        = "hdr-boundary-l5-v1"
	boundaryMaxWindows         = 336
	boundaryMaxFrames          = 1848
	boundaryMaxVCL             = 32768
	boundaryPreparationBytes   = (64 + 96 + 96 + 16) << 20
	boundaryParentBytes        = 128 << 20
)

// boundaryDecoderBytes follows the inspected 64-bit FFmpeg a5923073bf HEVC
// allocation geometry: all 32 physical DPB slots, thread/queue pictures, MV and
// reference-list tables, conversion scratch, packet copies and bounded pipes.
// Unknown decoder implementations are rejected by the sparse capability check.
func boundaryDecoderBytes(g boundaryGeometry, threads int) (int64, error) {
	if g.Width < 2 || g.Width > 8192 || g.Height < 2 || g.Height > 4320 || g.BitDepth != 10 ||
		g.MinCB < 8 || g.MinCB > 64 || g.CTB < 16 || g.CTB > 64 || g.MinCB > g.CTB ||
		g.CTB&(g.CTB-1) != 0 || g.MinCB&(g.MinCB-1) != 0 || threads < 1 || threads > 64 {
		return 0, errDecodedMapping
	}
	var failed bool
	mul := func(a, b int64) int64 {
		if a < 0 || b < 0 || b != 0 && a > math.MaxInt64/b {
			failed = true
			return 0
		}
		return a * b
	}
	add := func(values ...int64) int64 {
		var total int64
		for _, value := range values {
			if value < 0 || total > math.MaxInt64-value {
				failed = true
				return 0
			}
			total += value
		}
		return total
	}
	w, h, ctb, t := int64(g.Width), int64(g.Height), int64(g.CTB), int64(threads)
	pixels := mul(mul(add(w, 63)/64, 64), mul(add(h, 63)/64, 64))
	blocks := mul(add(w, ctb-1)/ctb, add(h, ctb-1)/ctb)
	frames := add(32, t, 8, 4)
	frameBytes := add(mul(3, pixels), pixels, mul(576, blocks), mul(576, 64))
	// Includes 2 MiB for the reduced colour planes, PQ LUT and conversion buffers.
	reservation := add(mul(frames, frameBytes), mul(mul(2, pixels), t), mul(12, pixels), 338<<20)
	if failed {
		return 0, errors.New("boundary decoder allocation overflow")
	}
	return max(reservation, boundaryPreparationBytes), nil
}

// boundaryConcurrency reduces worker count before decoder threads, under one
// operation allowance. The caller keeps this reservation through child cleanup.
func boundaryConcurrency(g boundaryGeometry, requested int, parentBytes int64) (int, int, int64, error) {
	if requested < 0 || requested > 64 || parentBytes < 0 || parentBytes > MaxRestoreBytes-boundaryParentBytes {
		return 0, 0, 0, errDecodedMapping
	}
	if requested == 0 {
		requested = 8
	}
	cpu := min(64, max(1, runtime.GOMAXPROCS(0)))
	for threads := min(requested, cpu); threads >= 1; threads-- {
		reservation, err := boundaryDecoderBytes(g, threads)
		if err != nil {
			return 0, 0, 0, err
		}
		available := int64(MaxRestoreBytes) - parentBytes - boundaryParentBytes
		workers := min(4, cpu/threads, int(available/reservation))
		if workers > 0 {
			return workers, threads, reservation, nil
		}
	}
	return 0, 0, 0, errors.New("boundary decoder exceeds shared operation memory")
}
