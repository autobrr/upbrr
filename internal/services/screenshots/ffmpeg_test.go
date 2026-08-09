// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
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

func TestCaptureFrameRecoversFromBlackFrameWithTimestampOffset(t *testing.T) {
	output := filepath.Join(t.TempDir(), "screen.png")
	blackPayload := testPNGBytes(t, color.RGBA{A: 255})
	validPayload := testPNGBytes(t, color.RGBA{
		R: 200,
		G: 100,
		B: 50,
		A: 255,
	})
	runner := &timestampSensitiveRunner{
		blackTimestamps: map[string]struct{}{"1.000": {}},
		blackPayload:    blackPayload,
		validPayload:    validPayload,
	}

	_, err := captureFrame(context.Background(), runner, "ffmpeg", captureRequest{
		InputPath:  "example.mkv",
		OutputPath: output,
		Timestamp:  1,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected black frame to recover with timestamp offset, got error: %v", err)
	}

	stat, statErr := os.Stat(output)
	if statErr != nil {
		t.Fatalf("expected output file to exist, got stat error: %v", statErr)
	}
	if stat.Size() == 0 {
		t.Fatal("expected non-empty output file")
	}

	wantTimestamps := []string{"1.000", "2.000"}
	if !slices.Equal(runner.timestamps, wantTimestamps) {
		t.Fatalf("attempted timestamps = %v, want %v", runner.timestamps, wantTimestamps)
	}
}

func TestCaptureFrameContinuesOffsetsAfterNoImage(t *testing.T) {
	output := filepath.Join(t.TempDir(), "screen.png")
	runner := &timestampSensitiveRunner{
		blackTimestamps: map[string]struct{}{"1.000": {}},
		noImageTimestamps: map[string]struct{}{
			"2.000": {},
			"3.000": {},
			"4.000": {},
			"6.000": {},
		},
		blackPayload: testPNGBytes(t, color.RGBA{A: 255}),
		validPayload: testPNGBytes(t, color.RGBA{R: 200, A: 255}),
	}

	_, err := captureFrame(context.Background(), runner, "ffmpeg", captureRequest{
		InputPath:  "example.mkv",
		OutputPath: output,
		Timestamp:  1,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected negative timestamp offset to recover after empty positive offsets, got error: %v", err)
	}

	wantTimestamps := []string{"1.000", "2.000", "3.000", "4.000", "6.000", "0.000"}
	if !slices.Equal(runner.timestamps, wantTimestamps) {
		t.Fatalf("attempted timestamps = %v, want %v", runner.timestamps, wantTimestamps)
	}
}

func TestCaptureFrameValidatesBlackSourceBeforeOverlay(t *testing.T) {
	output := filepath.Join(t.TempDir(), "screen.png")
	runner := &timestampSensitiveRunner{
		sourceBlackTimestamps: map[string]struct{}{"1.000": {}},
		blackPayload:          testPNGBytes(t, color.RGBA{A: 255}),
		validPayload:          testPNGBytes(t, color.RGBA{G: 200, A: 255}),
	}

	_, err := captureFrame(context.Background(), runner, "ffmpeg", captureRequest{
		InputPath:   "example.mkv",
		OutputPath:  output,
		Timestamp:   1,
		FrameOverlay: true,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected black source frame to recover before overlay, got error: %v", err)
	}

	wantTimestamps := []string{"1.000", "2.000", "2.000"}
	if !slices.Equal(runner.timestamps, wantTimestamps) {
		t.Fatalf("attempted timestamps = %v, want %v", runner.timestamps, wantTimestamps)
	}
	wantOverlays := []bool{false, false, true}
	if !slices.Equal(runner.overlays, wantOverlays) {
		t.Fatalf("overlay attempts = %v, want %v", runner.overlays, wantOverlays)
	}
}

func TestCaptureFrameHardFailsOnDecoderCorruption(t *testing.T) {
	output := filepath.Join(t.TempDir(), "screen.png")
	runner := &timestampSensitiveRunner{
		validPayload: testPNGBytes(t, color.RGBA{R: 200, A: 255}),
		stderr:       []byte("[h264] Error while decoding stream #0:0"),
	}

	_, err := captureFrame(context.Background(), runner, "ffmpeg", captureRequest{
		InputPath:     "example.mkv",
		OutputPath:    output,
		Timestamp:     1,
		UseLibplacebo: true,
		ToneMap:       true,
	}, api.NopLogger{})
	if !errors.Is(err, internalerrors.ErrFrameCorruption) {
		t.Fatalf("capture error = %v, want frame corruption", err)
	}
	if want := []string{"1.000"}; !slices.Equal(runner.timestamps, want) {
		t.Fatalf("attempted timestamps = %v, want %v", runner.timestamps, want)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("corrupt capture output survived: %v", statErr)
	}
}

func TestFFmpegFrameCorruptionIndicators(t *testing.T) {
	for _, diagnostic := range []string{
		"error while decoding",
		"Invalid data found when processing input",
		"non-existing PPS 0 referenced",
		"incomplete frame",
		"corrupt decoded frame in stream 0",
		"corrupt input packet in stream 0",
	} {
		if err := ffmpegFrameCorruptionError([]byte(diagnostic)); !errors.Is(err, internalerrors.ErrFrameCorruption) {
			t.Fatalf("diagnostic %q error = %v, want frame corruption", diagnostic, err)
		}
	}
}

type timestampSensitiveRunner struct {
	blackTimestamps       map[string]struct{}
	noImageTimestamps     map[string]struct{}
	sourceBlackTimestamps map[string]struct{}
	blackPayload          []byte
	validPayload          []byte
	stderr                []byte
	timestamps            []string
	overlays              []bool
}

func (r *timestampSensitiveRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	var ts string
	hasOverlay := false
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-ss":
			ts = args[i+1]
		case "-vf":
			hasOverlay = strings.Contains(args[i+1], "drawtext=")
		}
	}
	if ts != "" {
		r.timestamps = append(r.timestamps, ts)
		r.overlays = append(r.overlays, hasOverlay)
	}
	if _, noImage := r.noImageTimestamps[ts]; noImage {
		return CommandResult{ExitCode: 0}, nil
	}
	payload := r.validPayload
	if _, isBlack := r.blackTimestamps[ts]; isBlack {
		payload = r.blackPayload
	}
	if _, sourceIsBlack := r.sourceBlackTimestamps[ts]; sourceIsBlack && !hasOverlay {
		payload = r.blackPayload
	}
	if len(args) > 0 {
		if err := os.WriteFile(args[len(args)-1], payload, 0o600); err != nil {
			return CommandResult{ExitCode: 1}, fmt.Errorf("write output fixture: %w", err)
		}
	}
	return CommandResult{Stderr: r.stderr, ExitCode: 0}, nil
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
