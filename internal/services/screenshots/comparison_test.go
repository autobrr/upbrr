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
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonDiagnostics(pts int, picture string) string {
	return fmt.Sprintf("[Parsed_showinfo_1 @ 0x123] config in time_base: 1/1000, frame_rate: 10/1\n"+
		"[Parsed_showinfo_1 @ 0x123] n: 0 pts: %d pts_time:5.8 fmt:yuv420p i:P iskey:0 type:%s\n", pts, picture)
}

type comparisonInspectionRunner func(context.Context, string, []string, string) (CommandResult, error)

func (run comparisonInspectionRunner) Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error) {
	return run(ctx, name, args, dir)
}

func TestComparisonDiagnosticBoundsPreserveProvenance(t *testing.T) {
	diagnostic := &comparisonDiagnosticStream{}
	if _, err := diagnostic.Write([]byte(comparisonDiagnostics(5800, "B"))); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if _, err := diagnostic.Write([]byte(strings.Repeat("diagnostic ", 700) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	data, err := diagnostic.result()
	if err != nil {
		t.Fatal(err)
	}
	observed, err := comparisonObservations(data)
	if err != nil || len(observed) != 1 || observed[0].PTS != 5800 {
		t.Fatalf("provenance was evicted: %+v %v", observed, err)
	}
	if len(diagnostic.tail) > 64<<10 {
		t.Fatal("unbounded diagnostic tail")
	}
	for _, line := range []string{strings.Repeat("x", 8193), "[] showinfo n: 0 pts: 1\n"} {
		stream := &comparisonDiagnosticStream{}
		_, err := stream.Write([]byte(line))
		if len(line) > 8192 && err == nil {
			t.Fatal("oversized diagnostic line accepted")
		}
	}
	output := &comparisonOutputBuffer{limit: 4}
	if _, err := output.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, io.LimitReader(strings.NewReader("5"), 1)); err == nil || output.buffer.Len() != 4 {
		t.Fatal("decoder output limit was not enforced")
	}
}

func TestComparisonDecoderLimitTerminatesNativeProcess(t *testing.T) {
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native decoder limit test requires FFmpeg")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	runner := finiteComparisonRunner{base: commandRunner{}, outputLimit: 256}
	// The first native frame exceeds both the admitted output and the pipe's
	// normal write buffer. Termination cannot depend on tiny-frame batching.
	_, err = runner.Run(ctx, executable, []string{"-hide_banner", "-loglevel", "info", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=size=256x256:rate=100", "-threads", "1", "-vf", "showinfo", "-pix_fmt", "gray", "-flush_packets", "1", "-f", "rawvideo", "-"}, "")
	if !errors.Is(err, errComparisonDecoderLimit) || ctx.Err() != nil {
		t.Fatalf("output overflow did not terminate the owned decoder promptly: %v context=%v", err, ctx.Err())
	}
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

func TestComparisonInspectionRetainsFirstDecodedPictureFacts(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("native comparison inspection requires FFmpeg")
	}
	// This shared synthetic PQ fixture has reordered pictures. FFmpeg 6 feeds
	// ahead of its output limit unless inspection also bounds the filter input.
	path := filepath.Join("..", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv")
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil || source.NativePixelFormat != "yuv420p10le" || source.Interlace != "P" || source.StartTimeSeconds != 0 || source.FrameRate != 25 {
		t.Fatalf("first native picture facts were lost during bounded inspection: %+v %v", source, err)
	}
}

func TestComparisonCancelledCapturePreservesNativeFailures(t *testing.T) {
	source := comparisonTestSource(t)
	replayFailure := api.NewOperationError(api.OperationFailure{Code: api.OperationFailureInvalidSource, ComparisonReason: "replay_picture_changed"}, errors.New("synthetic replay mismatch"))
	for _, test := range []struct {
		name          string
		failure, want error
	}{
		{
			name:    "corruption",
			failure: internalerrors.ErrFrameCorruption,
			want:    internalerrors.ErrFrameCorruption,
		},
		{
			name:    "replay provenance",
			failure: replayFailure,
			want:    replayFailure,
		},
		{
			name:    "cancelled process",
			failure: ErrComparisonWorkerFailed,
			want:    context.Canceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			runner := comparisonInspectionRunner(func(context.Context, string, []string, string) (CommandResult, error) {
				calls++
				cancel()
				return CommandResult{Stderr: []byte(comparisonDiagnostics(5800, "B")), ExitCode: 1}, test.failure
			})
			service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
			_, err := service.ComparisonCapture(ctx, ComparisonCaptureRequest{Source: source, TimestampSeconds: .75})
			if !errors.Is(err, test.want) || calls != 1 {
				t.Fatalf("capture failure=%v calls=%d", err, calls)
			}
		})
	}
}

func TestComparisonSoftwareReplayRevalidatesHardwareWitness(t *testing.T) {
	source := comparisonTestSource(t)
	source.InputFingerprint = "synthetic-input"
	identity := api.ComparisonFrameIdentity{
		Version:             1,
		InputFingerprint:    source.InputFingerprint,
		Occurrence:          "file-0",
		StreamID:            "video-0",
		PTS:                 5800,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 1000,
	}
	png := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	for _, tc := range []struct {
		name, checksum string
		pts            int
		allowed        bool
	}{
		{
			name:     "exact native picture",
			checksum: "12345678",
			pts:      5800,
			allowed:  true,
		},
		{
			name:     "same timestamp different picture",
			checksum: "87654321",
			pts:      5800,
		},
		{
			name:     "same pixels different timestamp",
			checksum: "12345678",
			pts:      5840,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr := fmt.Sprintf("[Parsed_showinfo_1 @ synthetic] config in time_base: 1/1000, frame_rate: 25/1\n"+
				"[Parsed_showinfo_1 @ synthetic] n: 0 pts: %d pts_time:5.8 fmt:yuv420p sar:1/1 s:2x2 i:P type:B checksum:%s\n", tc.pts, tc.checksum)
			runner := comparisonInspectionRunner(func(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
				if slices.Contains(args, "-hwaccel") {
					t.Fatal("selected backend is unavailable to the injected runner")
				}
				return CommandResult{Stdout: png, Stderr: []byte(stderr)}, nil
			})
			s := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
			frame, err := s.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: 5.8,
				Replay: &ComparisonFrameReplay{
					Identity:        identity,
					Decoder:         "cuda",
					PictureChecksum: "12345678",
				},
			})
			if !tc.allowed {
				if err == nil || len(frame.ImageBytes) != 0 {
					t.Fatal("software published an unverified fallback picture")
				}
				return
			}
			if err != nil || !bytes.Equal(frame.ImageBytes, png) || frame.Replay == nil || frame.Replay.Decoder != "software" || *frame.Identity != identity {
				t.Fatalf("software lost the matched frame during revalidation: %v", err)
			}
		})
	}
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
			// A comparison-local opt-out wins over the ordinary screenshot setting.
			request.ToneMap = new(false)
			plainRunner := &scriptedRenderRunner{replies: []renderReply{{payload: valid, stderr: comparisonDiagnostics(5800, "B")}}}
			plain, err := NewService(cfg, api.NopLogger{}, t.TempDir(), plainRunner).ComparisonCapture(t.Context(), request)
			if err != nil || plain.Processing.Tonemapped || plain.Processing.RequestedRenderer != "none" || len(plainRunner.args) != 1 {
				t.Fatalf("local tone-mapping opt-out ignored: %+v err=%v", plain.Processing, err)
			}
			// Opt-in also works independently of a globally disabled setting.
			cfg := cfg
			cfg.ScreenshotHandling.ToneMap = false
			cfg.ScreenshotHandling.UseLibplacebo = false
			request.ToneMap = new(true)
			toneRunner := &scriptedRenderRunner{replies: []renderReply{{payload: valid, stderr: comparisonDiagnostics(5800, "B")}}}
			tone, err := NewService(cfg, api.NopLogger{}, t.TempDir(), toneRunner).ComparisonPreview(t.Context(), request)
			if err != nil || !tone.Processing.Tonemapped || tone.Processing.Renderer != "software" {
				t.Fatalf("local tone-mapping opt-in ignored: %+v err=%v", tone.Processing, err)
			}
		})
	}
}

func TestComparisonCaptionReportsResolutionAndActualToneMapping(t *testing.T) {
	source := comparisonTestSource(t)
	source.Width, source.Height = 4, 4
	source.HDRFacts = api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatHDR10},
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	for _, degraded := range []bool{false, true} {
		t.Run(strconv.FormatBool(degraded), func(t *testing.T) {
			cfg := config.Config{}
			cfg.ScreenshotHandling.ToneMap = true
			replies := []renderReply{}
			if degraded {
				replies = append(replies, renderReply{err: errors.New("converter unavailable")})
			}
			replies = append(replies, renderReply{payload: valid, stderr: comparisonDiagnostics(5800, "B")}, renderReply{payload: valid, stderr: comparisonDiagnostics(5800, "B")})
			runner := &scriptedRenderRunner{replies: replies}
			frame, err := NewService(cfg, api.NopLogger{}, t.TempDir(), runner).ComparisonCapture(t.Context(), ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: .75,
				OverlayLabel:     "Source B",
				ShowResolution:   true,
				OutputWidth:      2,
				OutputHeight:     2,
				ResizeOnly:       true,
			})
			if err != nil || frame.Processing.Tonemapped == degraded || frame.PTS != 5800 {
				t.Fatalf("frame=%+v err=%v", frame, err)
			}
			filter := ffmpegValueAfter(runner.args[len(runner.args)-1], "-vf")
			if !strings.Contains(filter, `Resolution\: 4 x 4 -> 2 x 2`) || !strings.Contains(filter, "text='Source B'") ||
				strings.Contains(filter, "- Tonemapped") == degraded {
				t.Fatalf("caption did not report actual dimensions and conversion outcome: %s", filter)
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
	runner := &scriptedRenderRunner{replies: []renderReply{
		{payload: valid, stderr: comparisonDiagnostics(5800, "P")},
		{payload: valid, stderr: comparisonDiagnostics(5800, "P")},
	}}
	source.StartTimeSeconds, source.FrameRate = 5, 10
	source.FrameCount = 100
	if _, err := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner).ComparisonCapture(t.Context(),
		ComparisonCaptureRequest{
			Source:           source,
			TimestampSeconds: 0.75,
			OverlayLabel:     "Source B",
		}); err != nil {
		t.Fatal(err)
	}
	filter := ffmpegValueAfter(runner.args[len(runner.args)-1], "-vf")
	frameLine, typeLine, labelLine := strings.Index(filter, "text='Frame 8 of 100'"), strings.Index(filter, `text='Picture type\: P'`), strings.Index(filter, "text='Source B'")
	if strings.Count(filter, "drawtext=") != 3 || frameLine < 0 || typeLine <= frameLine || labelLine <= typeLine {
		t.Fatalf("comparison caption must contain exactly the three requested lines in order: %s", filter)
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
	samples, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 1,
		StrideSeconds:   0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].PTS != -200 || samples[0].TimestampSeconds != -0.2 || samples[1].PTS != 0 ||
		samples[1].PictureType != "B" || len(samples[0].Pixels) != 256 || !bytes.Equal(samples[1].Pixels, pixels[256:]) {
		t.Fatalf("samples = %+v", samples)
	}
	if _, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 50000,
		StrideSeconds:   0.5,
	}); err == nil || len(runner.args) != 1 {
		t.Fatalf("unbounded sampling err=%v calls=%d", err, len(runner.args))
	}
	runner.replies[0].payload = pixels[:256]
	if _, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 1,
		StrideSeconds:   0.5,
	}); err == nil || errors.Is(err, ErrComparisonWorkerFailed) {
		t.Fatal("unmatched sample pixels were accepted")
	}
	for _, tc := range []struct {
		name   string
		reply  renderReply
		cancel bool
	}{
		{name: "empty timestamp window"},
		{name: "decoder failure", reply: renderReply{err: errors.New("synthetic decoder failure")}},
		{name: "corrupt frames", reply: renderReply{stderr: "error while decoding"}},
		{name: "malformed native provenance", reply: renderReply{stderr: strings.ReplaceAll(comparisonDiagnostics(0, "B"), "time_base: 1/1000", "time_base: 0/0")}},
		{name: "cancellation", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runner := &scriptedRenderRunner{replies: []renderReply{tc.reply}}
			if tc.cancel {
				runner.cancel = cancel
			}
			service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
			samples, err := service.SampleComparisonSource(ctx, ComparisonSampleRequest{
				Source:          source,
				DurationSeconds: 1,
				StrideSeconds:   0.5,
			})
			wantError := tc.reply.err != nil || tc.reply.stderr != "" || tc.cancel
			if len(samples) != 0 || (err != nil) != wantError || tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("empty sampling result=%+v err=%v", samples, err)
			}
			if errors.Is(err, ErrComparisonWorkerFailed) != (tc.reply.err != nil && !tc.cancel) {
				t.Fatalf("decoder stop and invalid evidence lost their distinct publication boundaries: %v", err)
			}
		})
	}
}

func TestComparisonSamplingNormalizesHDRWithoutLosingIdentity(t *testing.T) {
	source := comparisonTestSource(t)
	source.Width, source.Height = 1920, 1080
	source.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}}
	source.StartTimeSeconds = 5
	identity := comparisonDiagnostics(5800, "B")
	output := strings.ReplaceAll(identity, "Parsed_showinfo_1", "showinfo@comparison_output")
	pixels := bytes.Repeat([]byte{42}, 256)
	for _, tc := range []struct {
		name      string
		replies   []renderReply
		profile   string
		format    api.HDRFormat
		cancel    bool
		want      error
		wantError bool
		calls     int
	}{
		{
			name: "software-compatible HDR ignores final GPU preference",
			replies: []renderReply{
				{payload: pixels, stderr: identity + output},
			},
			calls: 1,
		},
		{
			name:    "wide-gamut SDR remains untonemapped",
			format:  api.HDRFormatWCG,
			replies: []renderReply{{payload: pixels, stderr: identity + output}},
			calls:   1,
		},
		{
			name: "converters unavailable",
			replies: []renderReply{
				{err: errors.New("unsupported")},
			},
			want:  ErrComparisonNormalizationUnavailable,
			calls: 1,
		},
		{
			name:    "Dolby Vision profile 5 needs decoded metadata",
			profile: "5",
			replies: []renderReply{
				{payload: pixels, stderr: identity + output},
			},
			want:  ErrComparisonNormalizationUnavailable,
			calls: 1,
		},
		{
			name:    "Dolby Vision profile 5 retains GPU native matching",
			profile: "5",
			replies: []renderReply{{payload: pixels, stderr: identity + output + "[Parsed_showinfo_1 @ synthetic] side data - Dolby Vision Metadata:\n"}},
			calls:   1,
		},
		{
			name: "changed output timestamp",
			replies: []renderReply{
				{payload: pixels, stderr: identity + strings.ReplaceAll(output, "5800", "5900")},
			},
			wantError: true,
			calls:     1,
		},
		{
			name: "missing output identity",
			replies: []renderReply{
				{payload: pixels, stderr: identity},
			},
			wantError: true,
			calls:     1,
		},
		{
			name:      "malformed native provenance never falls back",
			replies:   []renderReply{{payload: pixels, stderr: strings.ReplaceAll(identity, "time_base: 1/1000", "time_base: 0/0") + output}},
			wantError: true,
			calls:     1,
		},
		{
			name:    "diagnostic overflow never falls back",
			replies: []renderReply{{stderr: strings.Repeat("x", 8193)}},
			want:    errComparisonDecoderLimit,
			calls:   1,
		},
		{
			name: "corruption never falls back",
			replies: []renderReply{
				{stderr: "error while decoding"},
			},
			want:  internalerrors.ErrFrameCorruption,
			calls: 1,
		},
		{
			name:    "cancellation never falls back",
			cancel:  true,
			replies: []renderReply{{}},
			want:    context.Canceled,
			calls:   1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{}
			cfg.ScreenshotHandling.UseLibplacebo = true
			// Analysis conversion is independent of the final screenshot opt-out.
			cfg.ScreenshotHandling.ToneMap = false
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runner := &scriptedRenderRunner{replies: tc.replies}
			if tc.cancel {
				runner.cancel = cancel
			}
			source := source
			if tc.format != "" {
				source.HDRFacts.Formats = []api.HDRFormat{tc.format}
			}
			source.HDRFacts.DolbyVisionProfile = tc.profile
			if tc.profile != "" {
				source.HDRFacts.Formats = []api.HDRFormat{api.HDRFormatDolbyVision}
			}
			service := NewService(cfg, api.NopLogger{}, t.TempDir(), runner)
			if tc.profile == "5" {
				if crop, err := service.DetectComparisonActivePicture(ctx, source, 0, 4, nil); err != nil || crop != nil {
					t.Fatalf("optional border detection blocked metadata-aware matching: %v", err)
				}
			}
			samples, err := service.SampleComparisonSource(ctx, ComparisonSampleRequest{
				Source:          source,
				StartSeconds:    .75,
				DurationSeconds: .22,
				StrideSeconds:   .05,
				NormalizeSDR:    true,
			})
			if len(runner.args) != tc.calls || (err != nil) != (tc.want != nil || tc.wantError) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("samples=%+v calls=%d err=%v", samples, len(runner.args), err)
			}
			if tc.wantError && (errors.Is(err, ErrComparisonNormalizationUnavailable) || errors.Is(err, ErrComparisonWorkerFailed)) {
				t.Fatal("invalid frame identity permitted a fallback or partial publication")
			}
			if err == nil {
				if len(samples) != 1 || samples[0].PTS != 5800 || samples[0].TimeBaseDenominator != 1000 ||
					samples[0].PictureType != "B" || math.Abs(samples[0].RelativeTimestampSeconds-.8) > 1e-9 || !bytes.Equal(samples[0].Pixels, pixels) {
					t.Fatalf("normalization changed source identity or pixels: %+v", samples)
				}
				if tc.format == api.HDRFormatWCG {
					filter := ffmpegValueAfter(runner.args[0], "-vf")
					if strings.Contains(filter, "tonemap=") || strings.Contains(filter, "libplacebo=") {
						t.Fatalf("wide-gamut SDR received HDR tone mapping: %s", filter)
					}
				} else if tc.profile != "5" {
					filter := ffmpegValueAfter(runner.args[0], "-vf")
					if !strings.Contains(filter, "zscale=transfer=linear") || !strings.Contains(filter, "tonemap=tonemap=mobius") ||
						strings.Index(filter, "showinfo,") > strings.Index(filter, "tonemap=") || strings.Contains(filter, "setpts") {
						t.Fatalf("normalization bypassed the existing pipeline or changed the source clock: %s", filter)
					}
				}
			}
			for _, args := range runner.args {
				if ffmpegValueAfter(args, "-ss") != "0.750000000" || ffmpegValueAfter(args, "-map") != "0:V:0" ||
					!slices.Contains(args, "-an") || !strings.Contains(ffmpegValueAfter(args, "-vf"), "trim=start=5.750000000:end=5.970000000") {
					t.Fatalf("sampling lost its video-only window: %v", args)
				}
			}
		})
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
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=64x48:rate=10:duration=12",
		"-vf", "setpts=PTS+5/TB", "-c:v", "ffv1", path,
	}, ""); err != nil {
		t.Fatal(err)
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if source.Width != 64 || source.Height != 48 || source.FrameRate != 10 || source.DurationSeconds <= 0 || source.StartTimeSeconds != 5 || source.FrameCount != 120 {
		t.Fatalf("source facts = %+v", source)
	}
	for _, boundary := range []string{"bounded context", "caller cancellation", "output limit", "diagnostic limit"} {
		t.Run("inspection "+boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			runner := comparisonInspectionRunner(func(ctx context.Context, name string, args []string, dir string) (CommandResult, error) {
				called = true
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) > time.Minute {
					t.Fatal("file inspection started an unbounded decoder")
				}
				switch boundary {
				case "caller cancellation":
					cancel()
					return CommandResult{}, fmt.Errorf("stop inspection fixture: %w", ctx.Err())
				case "output limit":
					return CommandResult{Stdout: make([]byte, 65537)}, nil
				case "diagnostic limit":
					return CommandResult{Stderr: []byte(strings.Repeat("x", 8193))}, nil
				default:
					return (commandRunner{}).Run(ctx, name, args, dir)
				}
			})
			inspected, err := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner).InspectComparisonSource(ctx, path)
			if !called {
				t.Fatal("inspection did not reach the bounded native probe")
			}
			switch boundary {
			case "caller cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("inspection lost caller cancellation: %v", err)
				}
			case "output limit", "diagnostic limit":
				if !errors.Is(err, errComparisonDecoderLimit) {
					t.Fatalf("inspection accepted unbounded decoder output: %v", err)
				}
			default:
				if err != nil || inspected.StartTimeSeconds != source.StartTimeSeconds || inspected.NativePixelFormat != source.NativePixelFormat {
					t.Fatalf("bounded inspection changed source facts: %+v %v", inspected, err)
				}
			}
		})
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
	request.OutputWidth, request.OutputHeight = 96, 64
	scaled, err := service.ComparisonCapture(t.Context(), request)
	if err != nil || scaled.Width != 96 || scaled.Height != 64 || scaled.ComparisonFrameObservation != capture.ComparisonFrameObservation {
		t.Fatalf("scaled capture size=%dx%d identity=%+v err=%v", scaled.Width, scaled.Height, scaled.ComparisonFrameObservation, err)
	}
	scaledImage, _, err := image.Decode(bytes.NewReader(scaled.ImageBytes))
	if err != nil {
		t.Fatal(err)
	}
	// The 4:3 source fits inside 3:2 without stretching: centered side bars.
	for _, x := range []int{0, 95} {
		r, g, b, _ := scaledImage.At(x, 32).RGBA()
		if r != 0 || g != 0 || b != 0 {
			t.Fatal("scaling did not preserve aspect ratio with padding")
		}
	}
	request.OutputWidth, request.OutputHeight, request.ResizeOnly = 32, 24, true
	resized, err := service.ComparisonCapture(t.Context(), request)
	if err != nil || resized.Width != 32 || resized.Height != 24 || resized.ComparisonFrameObservation != capture.ComparisonFrameObservation {
		t.Fatalf("proportional downscaling changed native frame identity or geometry: %+v err=%v", resized, err)
	}
	request.ResizeOnly = false
	request.OutputWidth, request.OutputHeight = 0, 0
	request.OverlayLabel = "Source B"
	labelled, err := service.ComparisonCapture(t.Context(), request)
	if err != nil || labelled.ComparisonFrameObservation != capture.ComparisonFrameObservation || bytes.Equal(labelled.ImageBytes, capture.ImageBytes) {
		t.Fatalf("comparison label changed native frame identity: %v %+v", err, labelled.ComparisonFrameObservation)
	}
	var progress []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) { progress = append(progress, update) })
	samples, err := service.SampleComparisonSource(ctx, ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    0.75,
		DurationSeconds: 0.22,
		StrideSeconds:   0.05,
	})
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
	boundSource := source
	boundSource.InputFingerprint = "native-clock-replay"
	later, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          boundSource,
		StartSeconds:    8,
		DurationSeconds: .25,
		StrideSeconds:   .01,
	})
	if err != nil || len(later) == 0 || later[0].Replay == nil {
		t.Fatalf("missing later native witness: %v", err)
	}
	replay := ComparisonCaptureRequest{
		Source:           boundSource,
		TimestampSeconds: later[0].RelativeTimestampSeconds,
		Replay:           later[0].Replay,
	}
	actual, err := service.ComparisonCapture(t.Context(), replay)
	if err != nil || actual.PictureChecksum != later[0].PictureChecksum || actual.PTS != later[0].PTS {
		t.Fatalf("bounded replay changed the selected picture: %v", err)
	}
	replay.TimestampSeconds = 0
	if frame, err := service.ComparisonCapture(t.Context(), replay); err == nil || len(frame.ImageBytes) != 0 {
		t.Fatal("a stale file locator scanned beyond its bounded replay window")
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
	samples, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 0.5,
		StrideSeconds:   0.0001,
	})
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

func TestLocalComparisonHDRCaptureReplay(t *testing.T) {
	path := os.Getenv("UPBRR_COMPARISON_HDR_REPLAY_SOURCE")
	if path == "" {
		t.Skip("opt-in local GPU HDR replay acceptance")
	}
	moment, err := strconv.ParseFloat(os.Getenv("UPBRR_COMPARISON_HDR_REPLAY_SECONDS"), 64)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true, UseLibplacebo: true}}
	service := NewService(cfg, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil || !source.HDRFacts.SupportsToneMapping() {
		t.Fatalf("HDR source inspection unavailable: %v", err)
	}
	source.InputFingerprint = "local-hdr-replay"
	samples, err := service.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    max(0, moment-.3),
		DurationSeconds: 1,
		NativeFrames:    true,
		StrideSeconds:   .002,
		NormalizeSDR:    true,
	})
	if err != nil || len(samples) == 0 {
		t.Fatalf("native HDR sampling failed: %v", err)
	}
	selected := samples[0]
	for _, sample := range samples {
		if math.Abs(sample.RelativeTimestampSeconds-moment) < math.Abs(selected.RelativeTimestampSeconds-moment) {
			selected = sample
		}
	}
	frame, err := service.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
		Source:           source,
		TimestampSeconds: selected.RelativeTimestampSeconds,
		Replay:           selected.Replay,
		OverlayLabel:     "HDR source",
		OutputWidth:      1920,
		OutputHeight:     1080,
	})
	if err != nil || frame.PictureChecksum != selected.PictureChecksum || frame.PTS != selected.PTS || frame.TimeBaseDenominator != selected.TimeBaseDenominator ||
		frame.Processing.Renderer != "libplacebo" || !frame.Processing.Tonemapped || frame.Width != 1920 || frame.Height != 1080 {
		t.Fatalf("GPU replay lost its original picture, renderer or canvas: %v", err)
	}
}

func TestComparisonDisplayCountersUseMetadataWithoutIndexClaims(t *testing.T) {
	for _, tc := range []struct {
		name          string
		source        ComparisonSource
		seconds       float64
		number, total string
	}{
		{"nonzero origin and reported count", ComparisonSource{
			StartTimeSeconds: 5,
			FrameRate:        10,
			FrameCount:       20,
			DurationSeconds:  6.9,
		}, 5.8, "8", "20"},
		{"VFR metadata is a nominal display position", ComparisonSource{
			StartTimeSeconds:     5,
			FrameRate:            4,
			FrameCount:           4,
			VideoDurationSeconds: .9,
		}, 5.4, "1", "4"},
		{"video duration fallback excludes general origin", ComparisonSource{
			StartTimeSeconds:     5,
			FrameRate:            10,
			VideoDurationSeconds: 2,
			DurationSeconds:      7,
		}, 5.8, "8", "20"},
		{"unqualified general duration is not a count", ComparisonSource{
			StartTimeSeconds: 5,
			FrameRate:        10,
			DurationSeconds:  7,
		}, 5.8, "8", "unknown"},
		{"missing rate preserves reported total", ComparisonSource{FrameCount: 20}, .8, "unknown", "20"},
		{"inconsistent nominal rate cannot exceed total", ComparisonSource{FrameRate: 10, FrameCount: 4}, .9, "unknown", "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, total := comparisonDisplayCounters(tc.source, ComparisonFrameObservation{TimestampSeconds: tc.seconds})
			if number != tc.number || total != tc.total {
				t.Fatalf("display=%s of %s want=%s of %s", number, total, tc.number, tc.total)
			}
		})
	}
}
