// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonHardwareRetryPreservesWindowAdmissionAndFatalOutcomes(t *testing.T) {
	mutation := api.NewOperationError(api.OperationFailure{Code: api.OperationFailureInvalidSource, ComparisonReason: "source_changed"}, api.ErrActiveInputChanged)
	for _, test := range []struct {
		name, stderr, later string
		failure, want       error
		limit               float64
		calls               int
		cancel              bool
	}{
		{
			name:  "hardware success completes the original window on the first process",
			limit: 1,
			calls: 1,
		},
		{
			name:   "unavailable device initialization retries",
			stderr: "Device creation failed: -1313558101.",
			limit:  2,
			calls:  2,
		},
		{
			name:   "unsupported format retries",
			stderr: "[av1 @ synthetic] Hardware is lacking required capabilities",
			later:  "software",
			limit:  2,
			calls:  2,
		},
		{
			name:   "software surface at a hardware graph input retries",
			stderr: "Impossible to convert between the formats supported by the filter 'graph 0 input from stream 0:0' and the filter 'auto_scale_1'",
			later:  "software",
			limit:  2,
			calls:  2,
		},
		{
			name:    "graph failure after native frames remains fatal",
			stderr:  comparisonDiagnostics(5800, "B") + "Impossible to convert between the formats supported by the filter 'graph 0 input from stream 0:0' and the filter 'auto_scale_1'",
			failure: ErrComparisonWorkerFailed,
			want:    ErrComparisonWorkerFailed,
			limit:   2,
			calls:   1,
		},
		{
			name:   "typed device loss retries",
			stderr: "[hevc @ synthetic] CUDA_ERROR_CONTEXT_IS_DESTROYED",
			later:  "cuda",
			limit:  2,
			calls:  2,
		},
		{
			name:    "unclassified process failure remains fatal",
			failure: ErrComparisonWorkerFailed,
			want:    ErrComparisonWorkerFailed,
			limit:   2,
			calls:   1,
		},
		{
			name:   "corruption precedes device code",
			stderr: "error while decoding: CUDA_ERROR_CONTEXT_IS_DESTROYED",
			want:   internalerrors.ErrFrameCorruption,
			limit:  2,
			calls:  1,
		},
		{
			name:    "source mutation remains fatal",
			failure: mutation,
			want:    api.ErrActiveInputChanged,
			limit:   2,
			calls:   1,
		},
		{
			name:    "cancellation remains fatal",
			failure: context.Canceled,
			want:    context.Canceled,
			limit:   2,
			calls:   1,
		},
		{
			name:    "cancellation cannot hide corruption",
			failure: internalerrors.ErrFrameCorruption,
			want:    internalerrors.ErrFrameCorruption,
			limit:   2,
			calls:   1,
			cancel:  true,
		},
		{
			name:    "cancellation cannot hide source mutation",
			failure: mutation,
			want:    api.ErrActiveInputChanged,
			limit:   2,
			calls:   1,
			cancel:  true,
		},
		{
			name:    "cancellation cannot hide admission exhaustion",
			failure: ErrComparisonFastInconclusive,
			want:    ErrComparisonFastInconclusive,
			limit:   2,
			calls:   1,
			cancel:  true,
		},
		{
			name:   "cancellation cannot hide returned corruption evidence",
			stderr: "error while decoding",
			want:   internalerrors.ErrFrameCorruption,
			limit:  2,
			calls:  1,
			cancel: true,
		},
		{
			name:    "cancelled process exit remains cancellation",
			failure: ErrComparisonWorkerFailed,
			want:    context.Canceled,
			limit:   2,
			calls:   1,
			cancel:  true,
		},
		{
			name:   "cancelled device loss cannot retry",
			stderr: "[hevc @ synthetic] CUDA_ERROR_CONTEXT_IS_DESTROYED",
			want:   context.Canceled,
			limit:  2,
			calls:  1,
			cancel: true,
		},
		{
			name:   "retry cannot reset movie admission",
			stderr: "[hevc @ synthetic] CUDA_ERROR_CONTEXT_IS_DESTROYED",
			want:   ErrComparisonFastInconclusive,
			limit:  1,
			calls:  1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var commands [][]string
			runner := finiteComparisonRunner{outputLimit: 1024, base: comparisonInspectionRunner(func(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
				commands = append(commands, slices.Clone(args))
				if len(commands) == 1 {
					if test.failure == nil && test.stderr == "" {
						return CommandResult{Stdout: []byte("complete hardware output")}, nil
					}
					if test.cancel {
						cancel()
					}
					return CommandResult{
						Stdout:   []byte("partial hardware output"),
						Stderr:   []byte(test.stderr + "\n"),
						ExitCode: 1,
					}, test.failure
				}
				return CommandResult{Stdout: []byte("complete software output")}, nil
			})}
			s := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
			budget := &ComparisonDecodeBudget{RemainingEncodedBytes: 1024, MovieSecondsLimit: test.limit}
			request := ComparisonSampleRequest{
				DurationSeconds:     1,
				Budget:              budget,
				encodedAttemptBytes: 128,
			}
			args := []string{"-ss", "12.250000000", "-i", "Example.Native.mkv", "-vf", "trim=start=12.25:end=13.25,showinfo", "-f", "rawvideo", "-"}
			decoders := []comparisonDecoder{{
				backend:  "cuda",
				download: "hwdownload,format=nv12,",
				key:      t.TempDir(),
			}}
			result, err := s.runComparisonDecode(ctx, runner, "synthetic", args, "", decoders, &request)
			if !errors.Is(err, test.want) || len(commands) != test.calls {
				t.Fatalf("error=%v calls=%d", err, len(commands))
			}
			if budget.ConsumedMovieSeconds != float64(test.calls) || budget.RemainingEncodedBytes != 1024-int64(test.calls)*128 {
				t.Fatalf("physical decode was charged incorrectly: %+v", budget)
			}
			if !slices.Contains(commands[0], "cuda") {
				t.Fatal("first attempt did not use hardware")
			}
			if test.calls == 2 && (!slices.Equal(commands[1], args) || string(result.Stdout) != "complete software output" || result.comparisonBackend != "software") {
				t.Fatal("software retry changed the original window or retained partial hardware output")
			}
			if test.want == nil && test.calls == 1 && result.comparisonBackend != "cuda" {
				t.Fatal("successful hardware attempt lost its backend")
			}
			if test.later != "" {
				result, err := s.runComparisonDecode(ctx, runner, "synthetic", args, "", decoders, nil)
				if err != nil || result.comparisonBackend != test.later {
					t.Fatalf("later operation chose %s, want %s: %v", result.comparisonBackend, test.later, err)
				}
			}
		})
	}
}

func TestComparisonLibplaceboDoesNotInspectHardware(t *testing.T) {
	source := ComparisonSource{NativePixelFormat: "yuv420p", NativeCodec: "h264"}
	if decoders, err := comparisonDecoders(t.Context(), commandRunner{}, "", source, true); err != nil || len(decoders) != 0 {
		t.Fatalf("software-only decoding attempted hardware selection: %v", err)
	}
}

func TestComparisonHardwareInitializationDeadline(t *testing.T) {
	for _, firstPicture := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			started := time.Now()
			runner := finiteComparisonRunner{base: comparisonInspectionRunner(func(ctx context.Context, _ string, _ []string, _ string) (CommandResult, error) {
				if firstPicture {
					first, ok := ctx.Value(comparisonFirstNativeKey{}).(func())
					if !ok {
						t.Fatal("hardware initialization has no native observer")
					}
					diagnostic := &comparisonDiagnosticStream{firstNative: first}
					if _, err := diagnostic.Write([]byte("[showinfo@comparison_decoder_init @ synthetic] n: 0 pts: 0 checksum:0\n")); err != nil {
						t.Fatal(err)
					}
					// The selected picture can follow several seconds of preroll.
					time.Sleep(6 * time.Second)
					return CommandResult{}, context.Cause(ctx)
				}
				<-ctx.Done()
				return CommandResult{}, context.Cause(ctx)
			})}
			_, err := runComparisonHardwareDecode(t.Context(), runner, "synthetic", nil, "")
			if firstPicture {
				if err != nil || time.Since(started) != 6*time.Second {
					t.Fatalf("native progress kept the initialization timer active: %v", err)
				}
			} else if !errors.Is(err, errComparisonHardwareInitialization) || time.Since(started) != 3*time.Second {
				t.Fatalf("unavailable device exceeded initialization admission: %v", err)
			}
		})
	}
}

func TestComparisonNativeDecoderReplaysMatchedPicture(t *testing.T) {
	executable, err := resolveFFmpeg()
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	for _, fixture := range []struct {
		codec, format string
		hdr           bool
	}{{"libx264", "yuv420p", false}, {"libx264", "yuv420p10le", false}, {"libx265", "yuv420p10le", true}, {"mpeg2video", "yuv420p", false}} {
		t.Run(fixture.codec+"_"+fixture.format, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Example.Native.mkv")
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=2", "-vf", "format=" + fixture.format, "-c:v", fixture.codec, "-threads", "2", "-pix_fmt", fixture.format, "-color_range", "tv", "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709"}
			if fixture.hdr {
				args = append(args, "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-x265-params", "log-level=error:pools=1:frame-threads=1")
			}
			args = append(args, path)
			if output, err := exec.CommandContext(t.Context(), executable, args...).CombinedOutput(); err != nil {
				t.Skipf("synthetic encoder unavailable: %v %s", err, output)
			}
			s := NewService(config.Config{}, api.NopLogger{}, dir, nil)
			source, err := s.InspectComparisonSource(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, _ := api.CanonicalWorkflowFingerprint(path)
			source.InputFingerprint = string(fingerprint)
			if _, err := comparisonDecoders(t.Context(), commandRunner{}, executable, source, false); err != nil {
				t.Fatal(err)
			}
			request := ComparisonSampleRequest{
				Source:          source,
				StartSeconds:    .37,
				DurationSeconds: 1.25,
				StrideSeconds:   .002,
				NativeFrames:    true,
				NormalizeSDR:    true,
			}
			budget := &ComparisonDecodeBudget{MovieSecondsLimit: 8, RemainingEncodedBytes: 128 << 20}
			request.Budget = budget
			samples, err := s.SampleComparisonSource(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(samples) <= 25 || samples[25].Replay == nil || samples[25].Replay.File == nil {
				t.Fatal("matching did not retain the actual decoder")
			}
			selected := samples[25]
			if budget.ProcessStarts < 1 || budget.ConsumedMovieSeconds != float64(budget.ProcessStarts)*(request.DurationSeconds+.125) {
				t.Fatalf("physical sampling work not accounted for: %+v", budget)
			}
			request.Budget = nil
			settings := s.cfg
			settings.ScreenshotHandling.UseLibplacebo = true
			settings.ScreenshotHandling.ToneMap = true
			settings.ScreenshotHandling.TonemapAlgorithm = "reinhard"
			settings.ScreenshotHandling.Desat = 0
			changedSettings := NewService(settings, api.NopLogger{}, dir, nil)
			unchanged, err := changedSettings.SampleComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(samples, unchanged) {
				t.Fatalf("final capture settings changed analysis pixels: %v", err)
			}
			analysis, err := s.AnalyzeComparisonSource(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(analysis) <= 25 || analysis[25].Replay == nil || !analysis[25].Replay.SamePicture(selected.Replay) {
				t.Fatal("deep analysis did not retain the actual decoder")
			}
			unchanged, err = changedSettings.AnalyzeComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(analysis, unchanged) {
				t.Fatalf("final capture settings changed scene evidence: %v", err)
			}
			capture := ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: selected.RelativeTimestampSeconds,
				Replay:           selected.Replay,
			}
			capture.OverlayLabel = "Native test"
			frame, err := s.ComparisonCapture(t.Context(), capture)
			if err != nil {
				t.Fatal(err)
			}
			if frame.FrameID != selected.FrameID || !frame.Replay.SamePicture(selected.Replay) {
				t.Fatal("capture changed the retained native picture")
			}
			capture.Source.NativeCodec = "" // Exercise the same overlay with software decoding only.
			ctx, starts := comparisonProcessCounter(t.Context(), nil)
			frame, err = s.ComparisonCapture(ctx, capture)
			if err != nil || *starts != 1 || !frame.Replay.SamePicture(selected.Replay) {
				t.Fatalf("software overlay did not replay the picture in one process: starts=%d err=%v", *starts, err)
			}
		})
	}
}
