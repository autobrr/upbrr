// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type renderReply struct {
	payload []byte
	stderr  string
	err     error
}

type scriptedRenderRunner struct {
	replies []renderReply
	args    [][]string
	cancel  context.CancelFunc
}

func (r *scriptedRenderRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	index := len(r.args)
	r.args = append(r.args, slices.Clone(args))
	reply := r.replies[min(index, len(r.replies)-1)]
	result := CommandResult{Stdout: reply.payload, Stderr: []byte(reply.stderr)}
	if reply.err != nil {
		result.ExitCode = 1
	}
	output := args[len(args)-1]
	if output != "-" && len(reply.payload) > 0 {
		if err := os.WriteFile(output, reply.payload, 0o600); err != nil {
			return result, fmt.Errorf("write scripted capture: %w", err)
		}
	}
	if r.cancel != nil {
		r.cancel()
	}
	return result, reply.err
}

// Synthetic pixels test renderer policy and transport, not Dolby Vision colour quality.
func TestRenderRecoveryAgreesForPreviewAndCapture(t *testing.T) {
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	failure := errors.New("FFmpeg failed")
	unsupported := renderReply{
		stderr:  "No such filter: libplacebo",
		err:     failure,
		payload: []byte("partial"),
	}
	softwareFailed := renderReply{
		stderr:  "zscale: no path between colorspaces",
		err:     failure,
		payload: []byte("partial"),
	}
	device := renderReply{stderr: "Device creation failed", err: failure}
	p5 := api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, DolbyVisionProfile: "5"}
	p8 := api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}, DolbyVisionProfile: "8"}
	for _, test := range []struct {
		name         string
		toneMap, lib bool
		facts        api.HDRFacts
		replies      []renderReply
		modes        []string
		renderer     string
		degraded     bool
	}{
		{
			name:     "libplacebo primary",
			toneMap:  true,
			lib:      true,
			replies:  []renderReply{{payload: valid}},
			modes:    []string{"libplacebo"},
			renderer: "libplacebo",
		},
		{
			name:     "transient device retry",
			toneMap:  true,
			lib:      true,
			replies:  []renderReply{device, {payload: valid}},
			modes:    []string{"libplacebo", "libplacebo"},
			renderer: "libplacebo",
		},
		{
			name:     "HDR10 software fallback",
			toneMap:  true,
			lib:      true,
			facts:    api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}},
			replies:  []renderReply{unsupported, {payload: valid}},
			modes:    []string{"libplacebo", "software"},
			renderer: "software",
		},
		{
			name:     "compatible DV8 preserves software",
			toneMap:  true,
			lib:      true,
			facts:    p8,
			replies:  []renderReply{unsupported, {payload: valid}},
			modes:    []string{"libplacebo", "software"},
			renderer: "software",
		},
		{
			name:     "both tonemappers fail",
			toneMap:  true,
			lib:      true,
			replies:  []renderReply{unsupported, softwareFailed, {payload: valid}},
			modes:    []string{"libplacebo", "software", "none"},
			renderer: "none",
			degraded: true,
		},
		{
			name:     "profile5 metadata-aware primary",
			toneMap:  true,
			lib:      true,
			facts:    p5,
			replies:  []renderReply{{payload: valid, stderr: "[Parsed_showinfo_0 @ 0x123]   side data - Dolby Vision Metadata: rpu_type=2"}, {payload: valid, stderr: "[Parsed_showinfo_0 @ 0x123]   side data - Dolby Vision Metadata: rpu_type=2"}},
			modes:    []string{"libplacebo"},
			renderer: "libplacebo",
		},
		{
			name:     "profile5 missing decoded metadata despite decoy filename",
			toneMap:  true,
			lib:      true,
			facts:    p5,
			replies:  []renderReply{{payload: valid, stderr: "Input #0, matroska, from Dolby Vision Metadata.mkv; title: Dolby Vision Metadata"}, {payload: valid}},
			modes:    []string{"libplacebo", "none"},
			renderer: "none",
			degraded: true,
		},

		{
			name:     "profile5 skips software",
			toneMap:  true,
			lib:      true,
			facts:    p5,
			replies:  []renderReply{unsupported, {payload: valid}},
			modes:    []string{"libplacebo", "none"},
			renderer: "none",
			degraded: true,
		},
		{
			name:     "disabled libplacebo stays disabled",
			toneMap:  true,
			facts:    p5,
			replies:  []renderReply{{payload: valid}},
			modes:    []string{"none"},
			renderer: "none",
			degraded: true,
		},
		{
			name:     "SDR and explicit tonemapping off",
			lib:      true,
			facts:    p5,
			replies:  []renderReply{{payload: valid}},
			modes:    []string{"none"},
			renderer: "none",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := captureRequest{
				InputPath:     "Example.Release.mkv",
				Timestamp:     12.5,
				ToneMap:       test.toneMap,
				UseLibplacebo: test.lib,
				HDRFacts:      test.facts,
				Algorithm:     "reinhard",
				Desat:         0.25,
				SourceWidth:   720,
				SourceHeight:  480,
				WidthScale:    1.2,
				FrameOverlay:  true,
			}
			// Overlay source validation and final output must use the same selected mode.
			replies := slices.Clone(test.replies)
			replies = append(replies, renderReply{payload: valid})
			outcomes := make([]api.ScreenshotProcessing, 0, 2)
			for _, fileOutput := range []bool{false, true} {
				req := request
				if fileOutput {
					req.OutputPath = filepath.Join(t.TempDir(), "capture.png")
				}
				runner := &scriptedRenderRunner{replies: replies}
				logger := &screenshotRecordingLogger{}
				frame, err := renderFrame(t.Context(), runner, "ffmpeg", req, logger)
				if err != nil {
					t.Fatal(err)
				}
				outcomes = append(outcomes, frame.Processing)
				if frame.Timestamp != request.Timestamp || frame.Processing.Renderer != test.renderer || frame.Processing.Degraded != test.degraded || frame.Processing.Tonemapped != (test.renderer != "none") {
					t.Fatalf("outcome=%+v", frame)
				}
				var modes []string
				for _, args := range runner.args {
					filter := ffmpegValueAfter(args, "-vf")
					mode := "none"
					if strings.Contains(filter, "libplacebo=") {
						mode = "libplacebo"
					} else if strings.Contains(filter, "tonemap=") {
						mode = "software"
					}
					modes = append(modes, mode)
					if !strings.HasPrefix(filter, "trim=end_frame=1,") || ffmpegValueAfter(args, "-ss") != "12.500" || ffmpegValueAfter(args, "-i") != request.InputPath || !strings.Contains(filter, "scale=864:480") || slices.Contains(args, "-hwaccel") {
						t.Fatalf("frame/source/geometry/decode changed: %v", args)
					}
					if mode == "software" && !strings.Contains(filter, "tonemap=tonemap=reinhard:desat=0.25") {
						t.Fatalf("software preferences lost: %s", filter)
					}
					if mode == "libplacebo" && (!strings.Contains(filter, "apply_dolbyvision=1") || !strings.Contains(filter, "range=tv:format=rgb24,format=rgb24")) {
						t.Fatalf("metadata/PNG pipeline lost: %s", filter)
					}
				}
				wantModes := append(slices.Clone(test.modes), test.renderer)
				if !slices.Equal(modes, wantModes) {
					t.Fatalf("modes=%v want=%v", modes, wantModes)
				}
				if strings.Contains(ffmpegValueAfter(runner.args[len(runner.args)-2], "-vf"), "drawtext=") || !strings.Contains(ffmpegValueAfter(runner.args[len(runner.args)-1], "-vf"), "drawtext=") {
					t.Fatal("overlay preceded source validation")
				}
				if test.degraded && strings.Contains(ffmpegValueAfter(runner.args[len(runner.args)-1], "-vf"), "Tonemapped HDR") {
					t.Fatal("degraded image was labelled tonemapped")
				}
				if len(test.modes) > 1 || test.degraded {
					if !logger.Contains("Requested ") || frame.Processing.Warning == "" {
						t.Fatal("fallback missing ordinary warning")
					}
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("preview/capture outcomes differ: %+v", outcomes)
			}
		})
	}
}

func TestRenderRejectsInvalidOutputsAndStopsAtTerminalFailures(t *testing.T) {
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	for _, test := range []struct {
		name     string
		reply    renderReply
		cancel   bool
		want     error
		attempts int
	}{
		{
			name: "all unavailable",
			reply: renderReply{
				stderr:  "No such filter: tonemap",
				err:     errors.New("missing"),
				payload: []byte("partial"),
			},
			attempts: 3,
		},
		{
			name:     "no output",
			reply:    renderReply{},
			attempts: 3,
		},
		{
			name:     "malformed image",
			reply:    renderReply{payload: []byte("invalid")},
			attempts: 3,
		},
		{
			name:     "black image",
			reply:    renderReply{payload: testPNGBytes(t, color.RGBA{A: 255})},
			attempts: 3,
		},
		{
			name:     "decoder corruption",
			reply:    renderReply{payload: valid, stderr: "corrupt decoded frame"},
			want:     internalerrors.ErrFrameCorruption,
			attempts: 1,
		},
		{
			name:     "cancellation after image",
			reply:    renderReply{payload: valid},
			cancel:   true,
			want:     context.Canceled,
			attempts: 1,
		},
		{
			name:     "deadline error",
			reply:    renderReply{payload: valid, err: context.DeadlineExceeded},
			want:     context.DeadlineExceeded,
			attempts: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runner := &scriptedRenderRunner{replies: []renderReply{test.reply}}
			if test.cancel {
				runner.cancel = cancel
			}
			output := filepath.Join(t.TempDir(), "result.png")
			_, err := renderFrame(ctx, runner, "ffmpeg", captureRequest{
				InputPath:     "Example.Release.mkv",
				OutputPath:    output,
				Timestamp:     3,
				ToneMap:       true,
				UseLibplacebo: true,
			}, api.NopLogger{})
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if len(runner.args) != test.attempts {
				t.Fatalf("attempts=%d want=%d", len(runner.args), test.attempts)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial output survived: %v", err)
			}
		})
	}
}

func TestCaptureMixedBatchRecordsActualTonemapping(t *testing.T) {
	subject, _, _ := multiDiscScreenshotSubject(t)
	subject.Discs = subject.Discs[:1]
	subject.HDR = "HDR10"
	subject.MediaBinding = screenshotTestBinding(subject.SourcePath)
	root := t.TempDir()
	if err := writeTestBundledFFmpeg(root); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cfg := config.Config{}
	cfg.ScreenshotHandling.ToneMap = true
	cfg.ScreenshotHandling.UseLibplacebo = true
	cfg.ScreenshotHandling.FFmpegLimit = true
	cfg.ScreenshotHandling.ProcessLimit = 1
	valid := testPNGBytes(t, color.RGBA{R: 200, A: 255})
	fail := renderReply{
		stderr:  "No such filter",
		err:     errors.New("missing"),
		payload: []byte("partial"),
	}
	runner := &scriptedRenderRunner{replies: []renderReply{{payload: valid}, fail, fail, {payload: valid}}}
	service := NewServiceWithRepo(cfg, api.NopLogger{}, t.TempDir(), runner, nil)
	result, err := service.Capture(t.Context(), subject, []api.ScreenshotSelection{
		{
			DiscID:           subject.Discs[0].ID,
			Index:            0,
			TimestampSeconds: 12,
		},
		{
			DiscID:           subject.Discs[0].ID,
			Index:            1,
			TimestampSeconds: 24,
		},
	}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Images) != 2 || result.Tonemapped || !result.UsedLibplacebo {
		t.Fatalf("mixed result=%+v", result)
	}
	if result.Images[0].Tonemapping != api.ScreenshotTonemapped || result.Images[1].Tonemapping != api.ScreenshotNotTonemapped || result.Images[1].Processing == nil || !result.Images[1].Processing.Degraded {
		t.Fatalf("mixed processing=%+v", result.Images)
	}
}

func TestRecoveredCaptureTimestampSurvivesPlan(t *testing.T) {
	subject, _, _ := multiDiscScreenshotSubject(t)
	subject.Discs = subject.Discs[:1]
	subject.MediaBinding = screenshotTestBinding(subject.SourcePath)
	subject.ManualFrames = []int{288}
	root := t.TempDir()
	if err := writeTestBundledFFmpeg(root); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	repo := openScreenshotTestRepository(t)
	tmp := t.TempDir()
	runner := &scriptedRenderRunner{replies: []renderReply{
		{payload: testPNGBytes(t, color.RGBA{A: 255})},
		{payload: testPNGBytes(t, color.RGBA{R: 200, A: 255})},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmp, runner, repo)
	result, err := service.Capture(t.Context(), subject, []api.ScreenshotSelection{{
		DiscID:           subject.Discs[0].ID,
		Index:            0,
		TimestampSeconds: 12,
	}}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Images) != 1 || result.Images[0].TimestampSeconds != 14 {
		t.Fatalf("capture did not report resolved frame: %+v", result)
	}
	if err := service.SaveFinalSelections(t.Context(), subject, result.Images); err != nil {
		t.Fatal(err)
	}
	restarted := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmp, nil, repo)
	plan, err := restarted.Plan(t.Context(), subject, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.FinalSelections) != 1 || plan.FinalSelections[0].TimestampSeconds != 14 || len(plan.SuggestedSelections) != 0 {
		t.Fatalf("plan lost resolved frame: %+v", plan.FinalSelections)
	}
}
