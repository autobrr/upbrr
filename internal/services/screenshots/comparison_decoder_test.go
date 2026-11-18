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
	"sync"
	"sync/atomic"
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
		name, stderr  string
		failure, want error
		limit         float64
		calls         int
		cancel        bool
	}{
		{
			name:   "typed device loss retries",
			stderr: "[hevc @ synthetic] CUDA_ERROR_CONTEXT_IS_DESTROYED",
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
			result, _, err := s.runComparisonDecode(ctx, runner, "synthetic", args, "", comparisonDecoder{
				backend:  "cuda",
				download: "hwdownload,format=nv12,",
				key:      t.Name(),
			}, &request)
			if !errors.Is(err, test.want) || len(commands) != test.calls {
				t.Fatalf("error=%v calls=%d", err, len(commands))
			}
			if budget.ConsumedMovieSeconds != float64(test.calls) || budget.RemainingEncodedBytes != 1024-int64(test.calls)*128 {
				t.Fatalf("physical decode was charged incorrectly: %+v", budget)
			}
			if test.calls == 2 && (!slices.Equal(commands[1], args) || string(result.Stdout) != "complete software output") {
				t.Fatal("software retry changed the original window or retained partial hardware output")
			}
		})
	}
}

func TestComparisonDecoderProbeDeduplicationAndFailureScopes(t *testing.T) {
	s := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	key := t.Name()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func() (comparisonDecoder, error) {
		calls.Add(1)
		close(started)
		<-release
		return comparisonDecoder{key: key}, context.Cause(t.Context())
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			if _, err := s.admitComparisonDecoder(t.Context(), key, probe); err != nil {
				t.Error(err)
			}
		})
	}
	<-started
	close(release)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent first requests repeated decoder admission")
	}
	if _, err := s.admitComparisonDecoder(t.Context(), key, func() (comparisonDecoder, error) {
		t.Fatal("negative hardware decision was not cached")
		return comparisonDecoder{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{context.Canceled, ErrComparisonFastInconclusive, errors.New("synthetic provenance failure")} {
		failedKey := key + failure.Error()
		_, err := s.admitComparisonDecoder(t.Context(), failedKey, func() (comparisonDecoder, error) { return comparisonDecoder{}, failure })
		if !errors.Is(err, failure) {
			t.Fatal("decoder admission changed the original failure")
		}
		retried := false
		_, err = s.admitComparisonDecoder(t.Context(), failedKey, func() (comparisonDecoder, error) { retried = true; return comparisonDecoder{}, nil })
		if err != nil || !retried {
			t.Fatal("an attempt failure became a hardware capability receipt")
		}
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, ErrComparisonFastInconclusive, internalerrors.ErrFrameCorruption} {
		t.Run("concurrent owner failure "+failure.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failedKey := t.Name()
				started, release := make(chan struct{}), make(chan struct{})
				ownerDone, waiterDone := make(chan error, 1), make(chan error, 1)
				go func() {
					_, err := s.admitComparisonDecoder(t.Context(), failedKey, func() (comparisonDecoder, error) {
						close(started)
						<-release
						return comparisonDecoder{}, failure
					})
					ownerDone <- err
				}()
				<-started
				retried := false
				go func() {
					_, err := s.admitComparisonDecoder(t.Context(), failedKey, func() (comparisonDecoder, error) {
						retried = true
						return comparisonDecoder{}, nil
					})
					waiterDone <- err
				}()
				synctest.Wait()
				close(release)
				if err := <-ownerDone; !errors.Is(err, failure) {
					t.Fatalf("owner lost its own failure: %v", err)
				}
				err := <-waiterDone
				if errors.Is(failure, internalerrors.ErrFrameCorruption) {
					if retried || !errors.Is(err, failure) {
						t.Fatalf("waiter bypassed terminal evidence: retried=%t err=%v", retried, err)
					}
				} else if err != nil || !retried {
					t.Fatalf("active waiter inherited the owner's attempt failure: retried=%t err=%v", retried, err)
				}
			})
		})
	}
	// A required libplacebo operation must not inspect any executable or device.
	if decoder, err := s.comparisonDecoder(t.Context(), "", nil, "", ComparisonSampleRequest{}, "libplacebo"); err != nil || decoder.backend != "" {
		t.Fatalf("libplacebo attempted hardware admission: %v", err)
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
					first()
					time.Sleep(6 * time.Second)
					return CommandResult{}, context.Cause(ctx)
				}
				<-ctx.Done()
				return CommandResult{}, context.Cause(ctx)
			})}
			_, err := runComparisonHardwareProbe(t.Context(), runner, "synthetic", nil, "")
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

func TestComparisonNativeHardwareRequiresSoftwareWitnessEquality(t *testing.T) {
	executable, err := resolveFFmpeg()
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	for _, fixture := range []struct {
		codec, format string
		hdr           bool
	}{{"libx264", "yuv420p", false}, {"libx265", "yuv420p10le", true}} {
		t.Run(fixture.codec, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Example.Native.mkv")
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=2", "-vf", "format=" + fixture.format, "-c:v", fixture.codec, "-threads", "2", "-preset", "fast", "-pix_fmt", fixture.format, "-color_range", "tv", "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709"}
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
			request := ComparisonSampleRequest{
				Source:          source,
				StartSeconds:    .37,
				DurationSeconds: 1.25,
				StrideSeconds:   .002,
				NativeFrames:    true,
				NormalizeSDR:    true,
			}
			hardware, err := s.SampleComparisonSource(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			key, candidates, err := comparisonDecoderInputs(t.Context(), executable, source, "sampling-file")
			if err != nil {
				t.Fatal(err)
			}
			comparisonDecoderDecisions.Lock()
			entry := comparisonDecoderDecisions.values[key]
			comparisonDecoderDecisions.Unlock()
			if entry == nil && len(candidates) > 0 {
				t.Fatal("native decoder admission produced no scoped decision")
			}
			if entry != nil {
				t.Logf("sampling decoder=%s format=%s", entry.decoder.backend, source.NativePixelFormat)
			} else {
				t.Log("software host/device identity unavailable")
			}
			if key != "" {
				pinComparisonSoftware(comparisonDecoder{key: key})
			}
			software, err := s.SampleComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(hardware, software) {
				t.Fatalf("hardware/default output differs from canonical software pictures: %v", err)
			}
			settings := s.cfg
			settings.ScreenshotHandling.UseLibplacebo = true
			settings.ScreenshotHandling.ToneMap = true
			settings.ScreenshotHandling.TonemapAlgorithm = "reinhard"
			settings.ScreenshotHandling.Desat = 0
			changedSettings := NewService(settings, api.NopLogger{}, dir, nil)
			unchanged, err := changedSettings.SampleComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(software, unchanged) {
				t.Fatalf("final capture settings changed analysis pixels: %v", err)
			}
			analysis, err := s.AnalyzeComparisonSource(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			key, _, err = comparisonDecoderInputs(t.Context(), executable, source, "analysis-file")
			if err != nil {
				t.Fatal(err)
			}
			comparisonDecoderDecisions.Lock()
			entry = comparisonDecoderDecisions.values[key]
			comparisonDecoderDecisions.Unlock()
			if entry != nil {
				t.Logf("analysis decoder=%s", entry.decoder.backend)
			}
			pinComparisonSoftware(comparisonDecoder{key: key})
			control, err := s.AnalyzeComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(analysis, control) {
				t.Fatalf("hardware analysis changed native pixels or scene evidence: %v", err)
			}
			unchanged, err = changedSettings.AnalyzeComparisonSource(t.Context(), request)
			if err != nil || !reflect.DeepEqual(control, unchanged) {
				t.Fatalf("final capture settings changed scene evidence: %v", err)
			}
			capture := ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: hardware[25].RelativeTimestampSeconds,
				Replay:           hardware[25].Replay,
			}
			frame, err := s.ComparisonCapture(t.Context(), capture)
			if err != nil {
				t.Fatal(err)
			}
			key, _, err = comparisonDecoderInputs(t.Context(), executable, source, "capture-file")
			if err != nil {
				t.Fatal(err)
			}
			comparisonDecoderDecisions.Lock()
			entry = comparisonDecoderDecisions.values[key]
			comparisonDecoderDecisions.Unlock()
			if entry != nil {
				t.Logf("capture decoder=%s", entry.decoder.backend)
			}
			pinComparisonSoftware(comparisonDecoder{key: key})
			controlFrame, err := s.ComparisonCapture(t.Context(), capture)
			if err != nil || !reflect.DeepEqual(frame, controlFrame) {
				t.Fatalf("hardware capture changed exact replay: %v", err)
			}
		})
	}
}
