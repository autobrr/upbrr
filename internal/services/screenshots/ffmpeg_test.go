// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
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

	t.Chdir(root)

	got := bundledFFmpegPath()
	if got != path {
		t.Fatalf("bundledFFmpegPath() = %q, want %q", got, path)
	}
}

func TestBundledFFmpegPathReturnsEmptyWhenMissing(t *testing.T) {
	root := t.TempDir()

	t.Chdir(root)

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

func TestCaptureFrameBytesRejectsEmptySuccessfulOutput(t *testing.T) {
	runner := &singleResultRunner{result: CommandResult{ExitCode: 0}}

	payload, err := captureFrameBytes(context.Background(), runner, "ffmpeg", previewRequest{
		InputPath: "example.mkv",
		Timestamp: 1,
	}, api.NopLogger{})
	if err == nil {
		t.Fatal("expected empty ffmpeg stdout to fail")
	}
	if payload != nil {
		t.Fatalf("expected no preview payload, got %d bytes", len(payload))
	}
	if !strings.Contains(err.Error(), "ffmpeg produced no image") {
		t.Fatalf("expected no-image error, got %v", err)
	}
}

func TestCaptureFrameBytesRejectsBlackSuccessfulOutput(t *testing.T) {
	runner := &singleResultRunner{result: CommandResult{Stdout: testPNGBytes(t, color.RGBA{A: 255}), ExitCode: 0}}

	payload, err := captureFrameBytes(context.Background(), runner, "ffmpeg", previewRequest{
		InputPath: "example.mkv",
		Timestamp: 1,
	}, api.NopLogger{})
	if err == nil {
		t.Fatal("expected black ffmpeg stdout to fail")
	}
	if payload != nil {
		t.Fatalf("expected no preview payload, got %d bytes", len(payload))
	}
	if !strings.Contains(err.Error(), "ffmpeg produced black image") {
		t.Fatalf("expected black-image error, got %v", err)
	}
}

func TestCaptureFrameRejectsBlackOutputFile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "screen.png")
	runner := &writeOutputRunner{payload: testPNGBytes(t, color.RGBA{A: 255})}

	_, err := captureFrame(context.Background(), runner, "ffmpeg", captureRequest{
		InputPath:  "example.mkv",
		OutputPath: output,
		Timestamp:  1,
	}, api.NopLogger{})
	if err == nil {
		t.Fatal("expected black ffmpeg output file to fail")
	}
	if !strings.Contains(err.Error(), "ffmpeg produced black image") {
		t.Fatalf("expected black-image error, got %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatal("expected rejected black output file to be removed")
	}
}

type singleResultRunner struct {
	result CommandResult
	err    error
}

func (r *singleResultRunner) Run(context.Context, string, []string, string) (CommandResult, error) {
	return r.result, r.err
}

type writeOutputRunner struct {
	payload []byte
}

// writeOutputRunner emulates ffmpeg's file-output path so capture validation
// tests exercise the same disk read and cleanup code as production captures.
func (r *writeOutputRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	if len(args) > 0 {
		if err := os.WriteFile(args[len(args)-1], r.payload, 0o600); err != nil {
			return CommandResult{ExitCode: 1}, fmt.Errorf("write ffmpeg output fixture: %w", err)
		}
	}
	return CommandResult{ExitCode: 0}, nil
}

// testPNGBytes builds tiny decodable PNG frames for black-frame validation
// tests without depending on fixture files.
func testPNGBytes(t *testing.T, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, pixel)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
