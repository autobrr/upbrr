// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"runtime"
	"testing"
)

func TestBoundaryAllocationAdmission(t *testing.T) {
	g := boundaryGeometry{
		Width:    3840,
		Height:   2160,
		BitDepth: 10,
		MinCB:    8,
		CTB:      32,
	}
	old := runtime.GOMAXPROCS(20)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })
	workers, threads, reservation, err := boundaryConcurrency(g, 0, 0)
	if err != nil || workers != 1 || threads != 8 || reservation < 2453<<20 || reservation > 2455<<20 {
		t.Fatalf("4K admission: workers=%d threads=%d bytes=%d err=%v", workers, threads, reservation, err)
	}
	workers, threads, reservation, err = boundaryConcurrency(g, 64, 0)
	if err != nil || workers*threads > 20 || reservation+boundaryParentBytes > MaxRestoreBytes || threads >= 20 {
		t.Fatalf("override did not clamp: %d %d %d %v", workers, threads, reservation, err)
	}
	if _, _, _, err := boundaryConcurrency(g, 8, MaxRestoreBytes); err == nil {
		t.Fatal("admitted overcommitted parent")
	}
	g.CTB = 0
	if _, err := boundaryDecoderBytes(g, 8); err == nil {
		t.Fatal("accepted unknown SPS geometry")
	}
}
