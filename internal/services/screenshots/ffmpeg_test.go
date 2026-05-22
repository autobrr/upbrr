// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledFFmpegPathPrefersWorkingDirectory(t *testing.T) {
	folder := osFolder()
	if folder == "" {
		t.Skip("unsupported platform")
	}

	root := t.TempDir()
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	path := filepath.Join(root, "bin", "ffmpeg", folder, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	got := bundledFFmpegPath()
	if got != path {
		t.Fatalf("bundledFFmpegPath() = %q, want %q", got, path)
	}
}

func TestBundledFFmpegPathReturnsEmptyWhenMissing(t *testing.T) {
	root := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	if got := bundledFFmpegPath(); got != "" {
		t.Fatalf("bundledFFmpegPath() = %q, want empty string", got)
	}
}

func TestBuildFilterChainRoundsPARScaleToEven(t *testing.T) {
	filter := buildFilterChain(captureRequest{
		SourceWidth:  853,
		SourceHeight: 480,
		WidthScale:   1.0,
		HeightScale:  1.0,
	}, false)
	if strings.Contains(filter, "scale=") {
		t.Fatalf("expected square pixels to skip scale filter, got %q", filter)
	}

	filter = buildFilterChain(captureRequest{
		SourceWidth:  853,
		SourceHeight: 480,
		WidthScale:   1.0,
		HeightScale:  1.001,
	}, false)
	if !strings.HasPrefix(filter, "scale=854:480,") {
		t.Fatalf("expected scale dimensions rounded to even first in filter chain, got %q", filter)
	}
}

func TestRoundToEvenUsesNearestEvenForHalves(t *testing.T) {
	tests := map[float64]int{
		100.5: 100,
		101.5: 102,
		852.6: 854,
		853.0: 854,
	}
	for input, want := range tests {
		if got := roundToEven(input); got != want {
			t.Fatalf("roundToEven(%v) = %d, want %d", input, got, want)
		}
	}
}
