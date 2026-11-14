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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonDiagnostics(pts int, picture string) string {
	return fmt.Sprintf("[Parsed_showinfo_1 @ 0x123] config in time_base: 1/1000, frame_rate: 10/1\n"+
		"[Parsed_showinfo_1 @ 0x123] n: 0 pts: %d pts_time:5.8 fmt:yuv420p i:P iskey:0 type:%s\n", pts, picture)
}

func comparisonTestSource(t *testing.T) ComparisonSource {
	t.Helper()
	root := t.TempDir()
	if err := writeTestBundledFFmpeg(root); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	// Scripted runners must not depend on a host FFmpeg installation.
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(root, "Example.Movie.2026.mkv")
	if err := os.WriteFile(path, []byte("synthetic runner input"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ComparisonSource{Path: path}
}

func TestComparisonCapturePreservesObservedIdentityAcrossRendererFallback(t *testing.T) {
	source := comparisonTestSource(t)
	source.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}}
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	cfg := config.Config{}
	cfg.ScreenshotHandling.ToneMap = true
	cfg.ScreenshotHandling.UseLibplacebo = true
	cfg.ScreenshotHandling.FrameOverlay = true
	for _, preview := range []bool{false, true} {
		t.Run(strconv.FormatBool(preview), func(t *testing.T) {
			runner := &scriptedRenderRunner{replies: []renderReply{
				{stderr: comparisonDiagnostics(5800, "B") + "No such filter: libplacebo", err: errors.New("unsupported")},
				{payload: valid, stderr: comparisonDiagnostics(5800, "B")},
			}}
			service := NewService(cfg, api.NopLogger{}, t.TempDir(), runner)
			request := ComparisonCaptureRequest{Source: source, TimestampSeconds: 0.75}
			var frame ComparisonFrame
			var err error
			if preview {
				frame, err = service.ComparisonPreview(t.Context(), request)
			} else {
				frame, err = service.ComparisonCapture(t.Context(), request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if frame.RequestedTimestampSeconds != 0.75 || frame.PTS != 5800 || frame.TimestampSeconds != 5.8 ||
				frame.TimeBaseNumerator != 1 || frame.TimeBaseDenominator != 1000 || frame.PictureType != "B" ||
				frame.Processing.Renderer != "software" || !frame.Processing.Tonemapped || !bytes.Equal(frame.ImageBytes, valid) {
				t.Fatalf("comparison outcome: %+v", frame)
			}
			if len(runner.args) != 2 {
				t.Fatalf("attempts = %d", len(runner.args))
			}
			for _, args := range runner.args {
				filter := ffmpegValueAfter(args, "-vf")
				if !slices.Contains(args, "-copyts") || ffmpegValueAfter(args, "-ss") != "0.750000000" ||
					!strings.Contains(filter, "showinfo") || strings.Contains(filter, "drawtext") {
					t.Fatalf("comparison frame or overlay policy changed: %v", args)
				}
			}
		})
	}
}

func TestComparisonCaptureRejectsBlackMissingAndChangedIdentity(t *testing.T) {
	source := comparisonTestSource(t)
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	for _, test := range []struct {
		name    string
		label   string
		replies []renderReply
		calls   int
		want    error
	}{
		{
			name:    "black stays at requested frame",
			replies: []renderReply{{payload: testPNGBytes(t, color.RGBA{A: 255}), stderr: comparisonDiagnostics(5800, "P")}},
			calls:   1,
		},
		{
			name:    "missing observed PTS",
			replies: []renderReply{{payload: valid, stderr: "title: pts:5800 type:B"}},
			calls:   1,
		},
		{
			name:  "overlay cannot change decoded picture",
			label: "Source B",
			replies: []renderReply{
				{payload: valid, stderr: comparisonDiagnostics(5800, "P")},
				{payload: valid, stderr: comparisonDiagnostics(5900, "P")},
			},
			calls: 2,
		},
		{
			name:  "overlay cannot hide source corruption",
			label: "Source B",
			replies: []renderReply{
				{payload: valid, stderr: comparisonDiagnostics(5800, "P") + "error while decoding"},
				{payload: valid, stderr: comparisonDiagnostics(5800, "P")},
			},
			calls: 1,
			want:  internalerrors.ErrFrameCorruption,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRenderRunner{replies: test.replies}
			service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
			_, err := service.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: 0.75,
				OverlayLabel:     test.label,
			})
			if err == nil || len(runner.args) != test.calls {
				t.Fatalf("capture err=%v attempts=%d", err, len(runner.args))
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("capture error = %v, want %v", err, test.want)
			}
			for _, args := range runner.args {
				if ffmpegValueAfter(args, "-ss") != "0.750000000" {
					t.Fatalf("independent time recovery: %v", args)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), &scriptedRenderRunner{})
	if _, err := service.ComparisonCapture(ctx, ComparisonCaptureRequest{Source: source}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestComparisonSamplingRetainsSourceClockAndBounds(t *testing.T) {
	source := comparisonTestSource(t)
	source.StartTimeSeconds = -0.2
	pixels := bytes.Repeat([]byte{42}, 512)
	_, secondFrame, _ := strings.Cut(comparisonDiagnostics(0, "B"), "\n")
	runner := &scriptedRenderRunner{replies: []renderReply{{
		payload: pixels,
		stderr:  comparisonDiagnostics(-200, "I") + "frame=    1 fps=0.0\r" + secondFrame,
	}}}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	samples, err := service.SampleComparisonSource(t.Context(), source, 0, 1, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].PTS != -200 || samples[0].TimestampSeconds != -0.2 || samples[1].PTS != 0 ||
		samples[1].PictureType != "B" || len(samples[0].Pixels) != 256 || !bytes.Equal(samples[1].Pixels, pixels[256:]) {
		t.Fatalf("samples = %+v", samples)
	}
	if _, err := service.SampleComparisonSource(t.Context(), source, 0, 50000, 0.5); err == nil || len(runner.args) != 1 {
		t.Fatalf("unbounded sampling err=%v calls=%d", err, len(runner.args))
	}
	runner.replies[0].payload = pixels[:256]
	if _, err := service.SampleComparisonSource(t.Context(), source, 0, 1, 0.5); err == nil {
		t.Fatal("unmatched sample pixels were accepted")
	}
}

// Synthetic native media verifies decoded identity and time origin, not HDR fidelity.
func TestComparisonNativeSourceClockAndSampling(t *testing.T) {
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native comparison check requires FFmpeg")
	}
	path := filepath.Join(t.TempDir(), "Example.Offset.Movie.mkv")
	if _, err := (commandRunner{}).Run(t.Context(), executable, []string{
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=64x48:rate=10:duration=2",
		"-vf", "setpts=PTS+5/TB", "-c:v", "ffv1", path,
	}, ""); err != nil {
		t.Fatal(err)
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if source.Width != 64 || source.Height != 48 || source.FrameRate != 10 || source.DurationSeconds <= 0 || source.StartTimeSeconds != 5 {
		t.Fatalf("source facts = %+v", source)
	}
	request := ComparisonCaptureRequest{Source: source, TimestampSeconds: 0.75}
	preview, err := service.ComparisonPreview(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := service.ComparisonCapture(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.PTS != 5800 || preview.TimeBaseNumerator != 1 || preview.TimeBaseDenominator != 1000 || preview.TimestampSeconds != 5.8 ||
		math.Abs(preview.RelativeTimestampSeconds-0.8) > 1e-9 ||
		preview.PictureType != "I" || preview.ComparisonFrameObservation != capture.ComparisonFrameObservation || !bytes.Equal(preview.ImageBytes, capture.ImageBytes) {
		t.Fatalf("native preview=%+v capture=%+v", preview.ComparisonFrameObservation, capture.ComparisonFrameObservation)
	}
	// Independently select absolute source PTS to verify the adapter did not merely
	// return the same wrong seek from two entrypoints.
	witness, err := (commandRunner{}).Run(t.Context(), executable, []string{
		"-hide_banner", "-loglevel", "error", "-copyts", "-i", path, "-map", "0:V:0", "-an", "-sn", "-dn",
		"-vf", "select='gte(t,5.8)',format=rgb24", "-frames:v", "1", "-vcodec", "png", "-f", "image2pipe", "-",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	witnessImage, _, err := image.Decode(bytes.NewReader(witness.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	capturedImage, _, err := image.Decode(bytes.NewReader(capture.ImageBytes))
	if err != nil || !reflect.DeepEqual(witnessImage, capturedImage) {
		t.Fatalf("capture pixels differ from independently selected source frame: %v", err)
	}
	request.OverlayLabel = "Source B"
	labelled, err := service.ComparisonCapture(t.Context(), request)
	if err != nil || labelled.ComparisonFrameObservation != capture.ComparisonFrameObservation || bytes.Equal(labelled.ImageBytes, capture.ImageBytes) {
		t.Fatalf("comparison label changed native frame identity: %v %+v", err, labelled.ComparisonFrameObservation)
	}
	var progress []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) { progress = append(progress, update) })
	samples, err := service.SampleComparisonSource(ctx, source, 0.75, 0.22, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) < 2 || progress[0].Completed <= 0 || progress[0].Completed >= progress[0].Total ||
		progress[len(progress)-1].Status != api.StageStatusCompleted || progress[len(progress)-1].Completed != 1000 {
		t.Fatalf("sampling did not report live source-clock progress before validated completion: %+v", progress)
	}
	if len(samples) != 2 || samples[0].PTS != 5800 || samples[0].TimestampSeconds != 5.8 ||
		math.Abs(samples[0].RelativeTimestampSeconds-0.8) > 1e-9 || len(samples[0].Pixels) != 256 {
		t.Fatalf("native samples=%+v", samples)
	}
	for _, sample := range samples {
		if sample.PTS < 5750 || sample.PTS >= 5970 || sample.PictureType != "I" {
			t.Fatalf("sample escaped window or lost picture type: %+v", sample.ComparisonFrameObservation)
		}
	}
}

func TestComparisonNativeFractionalFrameRoundTrip(t *testing.T) {
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native comparison check requires FFmpeg")
	}
	path := filepath.Join(t.TempDir(), "Example.Fractional.Movie.mp4")
	if _, err := (commandRunner{}).Run(t.Context(), executable, []string{
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=24000/1001:duration=1",
		"-c:v", "libx264", "-threads", "1", "-video_track_timescale", "24000", path,
	}, ""); err != nil {
		t.Fatal(err)
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	samples, err := service.SampleComparisonSource(t.Context(), source, 0, 0.5, 0.0001)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) < 8 || samples[0].TimeBaseNumerator != 1 || samples[0].TimeBaseDenominator != 24000 {
		t.Fatalf("fractional-frame samples missing or wrong timebase: %+v", samples)
	}
	for _, sample := range samples[:8] {
		captured, err := service.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
			Source:           source,
			TimestampSeconds: sample.RelativeTimestampSeconds,
		})
		if err != nil {
			t.Fatal(err)
		}
		if captured.PTS != sample.PTS || captured.TimeBaseNumerator != sample.TimeBaseNumerator ||
			captured.TimeBaseDenominator != sample.TimeBaseDenominator || captured.PictureType != sample.PictureType {
			t.Fatalf("sampled picture changed after seeking to %.12f: sampled=%+v captured=%+v",
				sample.RelativeTimestampSeconds, sample.ComparisonFrameObservation, captured.ComparisonFrameObservation)
		}
	}
}
