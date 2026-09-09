// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMergeTrackerImagesIntoFinalSelectionsReindexesSparseIndices(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("png"), 0o600); err != nil {
			t.Fatalf("write temp image: %v", err)
		}
	}

	finalSelections := []api.ScreenshotImage{
		{Index: 0, Path: filepath.Join(tmpDir, "a.png")},
		{Index: 2, Path: filepath.Join(tmpDir, "b.png")},
		{Index: 5, Path: filepath.Join(tmpDir, "c.png")},
	}
	trackerLinks := []api.ScreenshotLinkedImage{
		{Path: filepath.Join(tmpDir, "c.png")},
		{Path: filepath.Join(tmpDir, "d.png")},
	}

	merged := mergeTrackerImagesIntoFinalSelections(finalSelections, trackerLinks)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged screenshots, got %d", len(merged))
	}
	for idx, image := range merged {
		if image.Index != idx {
			t.Fatalf("expected image %d to be reindexed to %d, got %d (%#v)", idx, idx, image.Index, merged)
		}
	}
	if merged[3].Path != filepath.Join(tmpDir, "d.png") {
		t.Fatalf("expected new tracker image appended after existing selections, got %#v", merged)
	}
}

func TestListExistingScreensExcludesDVDMenuAndPreviewFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := "Example.Release.2026.DVD-GRP"
	normalPath := filepath.Join(root, base+"-01-ss_1000.png")
	for _, imagePath := range []string{
		normalPath,
		filepath.Join(root, base+"-dvd-menu-01-123.png"),
		filepath.Join(root, base+"-preview-01.png"),
	} {
		if err := os.WriteFile(imagePath, []byte("synthetic image"), 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}

	images := listExistingScreens(root, base)
	if len(images) != 1 || images[0].Path != normalPath || images[0].Purpose != api.ScreenshotPurposeFinal {
		t.Fatalf("existing screenshots = %#v", images)
	}
}

func TestPlanUsesManualFrameOverridesWithoutDuration(t *testing.T) {
	tmpDir := t.TempDir()
	mediaInfoPath := filepath.Join(tmpDir, "mediainfo.json")
	payload := map[string]any{
		"media": map[string]any{
			"track": []map[string]any{
				{
					"@type":     "Video",
					"FrameRate": "24.000",
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal mediainfo: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, encoded, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	service := NewService(config.Config{}, api.NopLogger{}, tmpDir, nil)
	meta := api.ScreenshotSubject{
		SourcePath:        filepath.Join(tmpDir, "movie.mkv"),
		MediaInfoJSONPath: mediaInfoPath,
		ManualFrames:      []int{120, 360, 600},
	}

	plan, err := service.Plan(context.Background(), meta, 4)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.RequiresManualFrames {
		t.Fatalf("expected manual frame override to satisfy screenshot plan, got %#v", plan)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("expected 3 manual selections, got %#v", plan.SuggestedSelections)
	}
	if plan.SuggestedSelections[0].Frame != 120 || plan.SuggestedSelections[0].Source != "manual" {
		t.Fatalf("expected first manual selection, got %#v", plan.SuggestedSelections[0])
	}
}

func TestPlanProbesDurationWhenMediaInfoUnavailable(t *testing.T) {
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	tmpRoot := t.TempDir()
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &scriptedRunner{results: []CommandResult{{
		Stderr:   []byte("Duration: 00:10:00.000, start: 0.000000, bitrate: 1000 kb/s"),
		ExitCode: 0,
	}}}
	service := NewService(config.Config{}, api.NopLogger{}, tmpRoot, runner)
	plan, err := service.Plan(context.Background(), api.ScreenshotSubject{SourcePath: sourcePath}, 4)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.RequiresManualFrames || plan.DurationSeconds != 600 || len(plan.SuggestedSelections) != 4 {
		t.Fatalf("expected probed automatic plan, got %#v", plan)
	}
	if len(runner.calls) != 1 || ffmpegInputArg(runner.calls[0].args) != sourcePath {
		t.Fatalf("duration probe calls = %#v", runner.calls)
	}
}

func TestPlanUsesRequestedNormalScreenshotCountForEverySourceKind(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		discType string
		build    func(*testing.T) (string, string)
	}{
		{
			name: "file",
			build: func(t *testing.T) (string, string) {
				source := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
				if err := os.WriteFile(source, []byte("synthetic video"), 0o600); err != nil {
					t.Fatalf("write file source: %v", err)
				}
				return source, ""
			},
		},
		{
			name:     "BDMV",
			discType: "BDMV",
			build: func(t *testing.T) (string, string) {
				root := t.TempDir()
				video := filepath.Join(root, "00001.m2ts")
				if err := os.WriteFile(video, []byte("synthetic video"), 0o600); err != nil {
					t.Fatalf("write BDMV source: %v", err)
				}
				return root, video
			},
		},
		{
			name:     "DVD",
			discType: "DVD",
			build: func(t *testing.T) (string, string) {
				root := t.TempDir()
				videoTS := filepath.Join(root, "VIDEO_TS")
				if err := os.MkdirAll(videoTS, 0o700); err != nil {
					t.Fatalf("create VIDEO_TS: %v", err)
				}
				if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_1.VOB"), []byte("synthetic video"), 0o600); err != nil {
					t.Fatalf("write DVD source: %v", err)
				}
				return root, ""
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			sourcePath, videoPath := testCase.build(t)
			mediaInfoPath := filepath.Join(t.TempDir(), "mediainfo.json")
			payload := []byte(`{"media":{"track":[{"@type":"General","Duration":"600"},{"@type":"Video","FrameRate":"24.000"}]}}`)
			if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
				t.Fatalf("write mediainfo: %v", err)
			}
			service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
			plan, err := service.Plan(context.Background(), api.ScreenshotSubject{
				SourcePath:        sourcePath,
				VideoPath:         videoPath,
				DiscType:          testCase.discType,
				MediaInfoJSONPath: mediaInfoPath,
			}, 4)
			if err != nil {
				t.Fatalf("plan %s: %v", testCase.name, err)
			}
			if len(plan.SuggestedSelections) != 4 {
				t.Fatalf("%s normal screenshot count = %d, want 4", testCase.name, len(plan.SuggestedSelections))
			}
		})
	}
}

func TestPlanAllocatesScreenshotsAcrossEveryDisc(t *testing.T) {
	t.Parallel()

	subject, _, _ := multiDiscScreenshotSubject(t)
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	plan, err := service.Plan(context.Background(), subject, 5)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Discs) != 2 || len(plan.Discs[0].SuggestedSelections) != 3 || len(plan.Discs[1].SuggestedSelections) != 2 {
		t.Fatalf("disc plans = %#v", plan.Discs)
	}
	wantDiscIDs := []string{"disc-a", "disc-a", "disc-a", "disc-b", "disc-b"}
	if len(plan.SuggestedSelections) != len(wantDiscIDs) {
		t.Fatalf("suggestions = %#v", plan.SuggestedSelections)
	}
	for index, wantDiscID := range wantDiscIDs {
		if plan.SuggestedSelections[index].DiscID != wantDiscID {
			t.Fatalf("suggestion[%d] = %#v, want disc %q", index, plan.SuggestedSelections[index], wantDiscID)
		}
	}
}

func TestPlanAndCapturePreserveGoodDiscWhenAnotherDiscSetupFails(t *testing.T) {
	subject, _, _ := multiDiscScreenshotSubject(t)
	subject.Discs[0].Type = "DVD"
	subject.Discs[0].Root = filepath.Join(t.TempDir(), "missing-dvd")
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &writingScreenshotRunner{payload: testPNGBytes(t, color.RGBA{
		R: 16,
		G: 16,
		B: 16,
		A: 255,
	})}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	plan, err := service.Plan(context.Background(), subject, 2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Discs) != 2 || len(plan.Discs[0].SuggestedSelections) != 0 || len(plan.Discs[1].SuggestedSelections) != 1 {
		t.Fatalf("disc plans = %#v", plan.Discs)
	}
	if plan.DurationSeconds != 600 || plan.FrameRate != 24 || plan.RequiresManualFrames {
		t.Fatalf("aggregate timing/manual state = %#v", plan)
	}
	if len(plan.SuggestedSelections) != 1 || plan.SuggestedSelections[0].DiscID != "disc-b" {
		t.Fatalf("aggregate suggestions = %#v", plan.SuggestedSelections)
	}

	selections := append([]api.ScreenshotSelection{{
		DiscID:           "disc-a",
		Index:            0,
		TimestampSeconds: 10,
	}}, plan.SuggestedSelections...)
	result, err := service.Capture(context.Background(), subject, selections, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(result.Images) != 1 || result.Images[0].DiscID != "disc-b" || len(result.Errors) != 1 || result.Errors[0].DiscID != "disc-a" {
		t.Fatalf("minimum-one Plan to Capture result = %#v", result)
	}
}

func TestPlanReturnsHardDiscDurationProbeCorruption(t *testing.T) {
	subject, _, videoB := multiDiscScreenshotSubject(t)
	subject.Discs[1].MediaInfoJSONPath = ""
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &scriptedRunner{results: []CommandResult{{
		Stderr:   []byte("corrupt input packet in stream 0"),
		ExitCode: 1,
	}}}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	_, err := service.Plan(context.Background(), subject, 2)
	if !errors.Is(err, internalerrors.ErrFrameCorruption) {
		t.Fatalf("plan error = %v, want frame corruption", err)
	}
	if len(runner.calls) != 1 || ffmpegInputArg(runner.calls[0].args) != videoB {
		t.Fatalf("duration probe calls = %#v", runner.calls)
	}
}

func TestPlanExpandsRawManualFramesAcrossEveryDisc(t *testing.T) {
	t.Parallel()

	subject, _, _ := multiDiscScreenshotSubject(t)
	subject.ManualFrames = []int{24, 48}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	plan, err := service.Plan(context.Background(), subject, 2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantDiscIDs := []string{"disc-a", "disc-a", "disc-b", "disc-b"}
	wantFrames := []int{24, 48, 24, 48}
	if len(plan.SuggestedSelections) != len(wantDiscIDs) {
		t.Fatalf("suggestions = %#v", plan.SuggestedSelections)
	}
	for index := range wantDiscIDs {
		selection := plan.SuggestedSelections[index]
		if selection.DiscID != wantDiscIDs[index] || selection.Frame != wantFrames[index] || selection.Source != "manual" {
			t.Fatalf("suggestion[%d] = %#v", index, selection)
		}
	}
}

func TestMultiDiscPreviewRequiresDiscAndCaptureNamesDoNotCollide(t *testing.T) {
	subject, videoA, videoB := multiDiscScreenshotSubject(t)
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	payload := testPNGBytes(t, color.RGBA{
		R: 16,
		G: 16,
		B: 16,
		A: 255,
	})
	previewRunner := &scriptedRunner{results: []CommandResult{{Stdout: payload, ExitCode: 0}}}
	previewService := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), previewRunner)
	if _, err := previewService.PreviewFrame(context.Background(), subject, "", 10); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("preview without disc error = %v", err)
	}
	if _, err := previewService.PreviewFrame(context.Background(), subject, "missing-disc", 10); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("preview with unknown disc error = %v", err)
	}
	preview, err := previewService.PreviewFrame(context.Background(), subject, "disc-b", 10)
	if err != nil {
		t.Fatalf("preview disc-b: %v", err)
	}
	if preview.DiscID != "disc-b" || preview.DiscName != "Disc 2" || len(previewRunner.calls) != 1 ||
		ffmpegInputArg(previewRunner.calls[0].args) != videoB {
		t.Fatalf("preview = %#v calls=%#v", preview, previewRunner.calls)
	}

	captureRunner := &writingScreenshotRunner{payload: payload}
	captureService := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), captureRunner)
	result, err := captureService.Capture(context.Background(), subject, []api.ScreenshotSelection{
		{
			DiscID:           "disc-a",
			Index:            0,
			TimestampSeconds: 10,
		},
		{
			DiscID:           "disc-b",
			Index:            0,
			TimestampSeconds: 10,
		},
	}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(result.Images) != 2 || result.Images[0].Path == result.Images[1].Path {
		t.Fatalf("capture images = %#v", result.Images)
	}
	if result.Images[0].DiscID != "disc-a" || result.Images[1].DiscID != "disc-b" ||
		!strings.Contains(filepath.Base(result.Images[0].Path), "disc-disc-a-") ||
		!strings.Contains(filepath.Base(result.Images[1].Path), "disc-disc-b-") {
		t.Fatalf("disc-scoped capture images = %#v", result.Images)
	}
	if len(captureRunner.calls) != 2 || ffmpegInputArg(captureRunner.calls[0].args) != videoA || ffmpegInputArg(captureRunner.calls[1].args) != videoB {
		t.Fatalf("capture calls = %#v", captureRunner.calls)
	}
}

func TestCapturePreservesSuccessfulDiscsWhenDiscSetupFails(t *testing.T) {
	subject, _, _ := multiDiscScreenshotSubject(t)
	subject.Discs[1].Type = "DVD"
	subject.Discs[1].Root = filepath.Join(t.TempDir(), "missing-dvd")
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &writingScreenshotRunner{payload: testPNGBytes(t, color.RGBA{
		R: 16,
		G: 16,
		B: 16,
		A: 255,
	})}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	result, err := service.Capture(context.Background(), subject, []api.ScreenshotSelection{
		{
			DiscID:           "disc-a",
			Index:            0,
			TimestampSeconds: 10,
		},
		{
			DiscID:           "disc-b",
			Index:            1,
			TimestampSeconds: 20,
		},
		{
			DiscID:           "disc-b",
			Index:            2,
			TimestampSeconds: 30,
		},
	}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(result.Images) != 1 || result.Images[0].DiscID != "disc-a" {
		t.Fatalf("successful disc images = %#v", result.Images)
	}
	if len(result.Errors) != 2 {
		t.Fatalf("failed disc errors = %#v", result.Errors)
	}
	for offset, captureErr := range result.Errors {
		if captureErr.DiscID != "disc-b" || captureErr.Index != offset+1 || strings.TrimSpace(captureErr.Message) == "" {
			t.Fatalf("failed disc error[%d] = %#v", offset, captureErr)
		}
		if strings.Contains(captureErr.Message, subject.Discs[1].Root) {
			t.Fatalf("failed disc error exposed local path: %q", captureErr.Message)
		}
	}
}

func TestCaptureReturnsHardDiscCorruptionAfterSuccessfulDisc(t *testing.T) {
	subject, _, videoB := multiDiscScreenshotSubject(t)
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &aggregateCaptureRunner{
		payload: testPNGBytes(t, color.RGBA{
			R: 16,
			G: 16,
			B: 16,
			A: 255,
		}),
		corruptInput: videoB,
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	result, err := service.Capture(context.Background(), subject, []api.ScreenshotSelection{
		{
			DiscID:           "disc-a",
			Index:            0,
			TimestampSeconds: 10,
		},
		{
			DiscID:           "disc-b",
			Index:            0,
			TimestampSeconds: 10,
		},
	}, api.ScreenshotPurposeFinal)
	if !errors.Is(err, internalerrors.ErrFrameCorruption) {
		t.Fatalf("capture error = %v, want frame corruption", err)
	}
	if len(result.Images) != 1 || result.Images[0].DiscID != "disc-a" || len(result.Errors) != 0 {
		t.Fatalf("partial hard-error result = %#v", result)
	}
}

func TestCaptureReturnsHardCorruptionFromDVDDurationProbe(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	for _, name := range []string{"VTS_01_1.VOB", "VTS_01_2.VOB"} {
		if err := os.WriteFile(filepath.Join(videoTS, name), []byte("synthetic video"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mediaInfoPath := filepath.Join(root, "mediainfo.json")
	if err := os.WriteFile(
		mediaInfoPath,
		[]byte(`{"media":{"track":[{"@type":"General","Duration":"120"},{"@type":"Video","FrameRate":"24.000"}]}}`),
		0o600,
	); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &scriptedRunner{results: []CommandResult{{
		Stderr:   []byte("corrupt input packet in stream 0"),
		ExitCode: 1,
	}}}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	_, err := service.Capture(context.Background(), api.ScreenshotSubject{
		SourcePath:        root,
		DiscType:          "DVD",
		MediaInfoJSONPath: mediaInfoPath,
	}, []api.ScreenshotSelection{{Index: 0, TimestampSeconds: 10}}, api.ScreenshotPurposeFinal)
	if !errors.Is(err, internalerrors.ErrFrameCorruption) {
		t.Fatalf("capture error = %v, want frame corruption", err)
	}
	if len(runner.calls) != 1 || ffmpegInputArg(runner.calls[0].args) != filepath.Join(videoTS, "VTS_01_1.VOB") {
		t.Fatalf("duration probe calls = %#v", runner.calls)
	}
}

func TestPreviewFrameExcludesDVDMenuVOB(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_0.VOB"), []byte("m"), 0o600); err != nil {
		t.Fatalf("write zero VOB: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_1.VOB"), []byte(strings.Repeat("c", 99)), 0o600); err != nil {
		t.Fatalf("write content VOB: %v", err)
	}
	mediaInfoPath := filepath.Join(root, "mediainfo.json")
	payload := []byte(`{"media":{"track":[{"@type":"General","Duration":"100"},{"@type":"Video","FrameRate":"25.000"}]}}`)
	if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &scriptedRunner{results: []CommandResult{{
		Stdout: testPNGBytes(t, color.RGBA{
			R: 16,
			G: 16,
			B: 16,
			A: 255,
		}),
		ExitCode: 0,
	}}}
	service := NewService(config.Config{}, api.NopLogger{}, root, runner)
	preview, err := service.PreviewFrame(context.Background(), api.ScreenshotSubject{
		SourcePath:        root,
		DiscType:          "DVD",
		MediaInfoJSONPath: mediaInfoPath,
	}, "", 0.5)
	if err != nil {
		t.Fatalf("preview frame: %v", err)
	}
	if len(preview.ImageBytes) == 0 {
		t.Fatal("expected preview payload")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one ffmpeg call, got %#v", runner.calls)
	}
	if got := ffmpegInputArg(runner.calls[0].args); got != filepath.Join(videoTS, "VTS_01_1.VOB") {
		t.Fatalf("expected preview call to use content VOB, got %q", got)
	}
	if got := ffmpegValueAfter(runner.calls[0].args, "-ss"); got != "0.500" {
		t.Fatalf("expected content seek at 0.500, got %q", got)
	}
}

func TestPreviewFrameTonemapsHDR(t *testing.T) {
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &scriptedRunner{results: []CommandResult{{
		Stdout: testPNGBytes(t, color.RGBA{
			R: 16,
			G: 16,
			B: 16,
			A: 255,
		}),
		ExitCode: 0,
	}}}
	service := NewService(config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{
		ToneMap:          true,
		TonemapAlgorithm: "hable",
		Desat:            0.25,
	}}, api.NopLogger{}, t.TempDir(), runner)
	_, err := service.PreviewFrame(context.Background(), api.ScreenshotSubject{
		SourcePath: "Example.Release.2026.1080p-GRP.mkv",
		HDR:        "HDR10",
	}, "", 1)
	if err != nil {
		t.Fatalf("preview frame: %v", err)
	}

	want := "zscale=transfer=linear,tonemap=tonemap=hable:desat=0.25,zscale=transfer=bt709,format=rgb24"
	if got := ffmpegValueAfter(runner.calls[0].args, "-vf"); got != want {
		t.Fatalf("preview filter = %q, want %q", got, want)
	}
}

func TestCaptureReturnsHardErrorAndCancelsSiblingFramesForCorruption(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	mediaInfoPath := filepath.Join(root, "mediainfo.json")
	if err := os.WriteFile(mediaInfoPath, []byte(`{"media":{"track":[{"@type":"General","Duration":"100"},{"@type":"Video","FrameRate":"25.000"}]}}`), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)

	runner := &corruptionCancelRunner{allStarted: make(chan struct{})}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := service.Capture(ctx, api.ScreenshotSubject{
		SourcePath:        sourcePath,
		MediaInfoJSONPath: mediaInfoPath,
	}, []api.ScreenshotSelection{
		{Index: 0, TimestampSeconds: 10},
		{Index: 1, TimestampSeconds: 20},
		{Index: 2, TimestampSeconds: 30},
		{Index: 3, TimestampSeconds: 40},
	}, api.ScreenshotPurposeFinal)
	if !errors.Is(err, internalerrors.ErrFrameCorruption) {
		t.Fatalf("capture error = %v, want frame corruption", err)
	}
	if got := runner.canceled.Load(); got != 3 {
		t.Fatalf("canceled sibling captures = %d, want 3", got)
	}
}

type runnerCall struct {
	args []string
}

type scriptedRunner struct {
	results []CommandResult
	calls   []runnerCall
}

type writingScreenshotRunner struct {
	payload []byte
	calls   []runnerCall
}

type aggregateCaptureRunner struct {
	payload      []byte
	corruptInput string
}

type corruptionCancelRunner struct {
	started    atomic.Int32
	canceled   atomic.Int32
	allStarted chan struct{}
}

func (r *corruptionCancelRunner) Run(ctx context.Context, _ string, args []string, _ string) (CommandResult, error) {
	if r.started.Add(1) == 4 {
		close(r.allStarted)
	}
	select {
	case <-r.allStarted:
	case <-ctx.Done():
		r.canceled.Add(1)
		return CommandResult{ExitCode: 1}, fmt.Errorf("synthetic ffmpeg start: %w", ctx.Err())
	}
	if ffmpegValueAfter(args, "-ss") == "10.000" {
		return CommandResult{Stderr: []byte("corrupt decoded frame in stream 0"), ExitCode: 0}, nil
	}
	<-ctx.Done()
	r.canceled.Add(1)
	return CommandResult{ExitCode: 1}, fmt.Errorf("synthetic ffmpeg canceled: %w", ctx.Err())
}

func (r *scriptedRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	r.calls = append(r.calls, runnerCall{args: append([]string(nil), args...)})
	if len(r.results) == 0 {
		return CommandResult{ExitCode: 1}, errors.New("unexpected ffmpeg runner call")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

func (r *writingScreenshotRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	r.calls = append(r.calls, runnerCall{args: append([]string(nil), args...)})
	if len(args) == 0 {
		return CommandResult{ExitCode: 1}, errors.New("missing screenshot output")
	}
	if err := os.WriteFile(args[len(args)-1], r.payload, 0o600); err != nil {
		return CommandResult{ExitCode: 1}, fmt.Errorf("write screenshot output: %w", err)
	}
	return CommandResult{ExitCode: 0}, nil
}

func (r *aggregateCaptureRunner) Run(_ context.Context, _ string, args []string, _ string) (CommandResult, error) {
	if ffmpegInputArg(args) == r.corruptInput {
		return CommandResult{Stderr: []byte("corrupt decoded frame in stream 0"), ExitCode: 0}, nil
	}
	if len(args) == 0 {
		return CommandResult{ExitCode: 1}, errors.New("missing screenshot output")
	}
	if err := os.WriteFile(args[len(args)-1], r.payload, 0o600); err != nil {
		return CommandResult{ExitCode: 1}, fmt.Errorf("write screenshot output: %w", err)
	}
	return CommandResult{ExitCode: 0}, nil
}

func multiDiscScreenshotSubject(t *testing.T) (api.ScreenshotSubject, string, string) {
	t.Helper()
	collectionRoot := t.TempDir()
	mediaInfoPayload := []byte(`{"media":{"track":[{"@type":"General","Duration":"600"},{"@type":"Video","FrameRate":"24.000"}]}}`)
	discs := make([]api.ScreenshotDiscSubject, 0, 2)
	videos := make([]string, 0, 2)
	for index, discID := range []string{"disc-a", "disc-b"} {
		root := filepath.Join(collectionRoot, fmt.Sprintf("Disc %d", index+1))
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatalf("create disc root: %v", err)
		}
		videoPath := filepath.Join(root, fmt.Sprintf("video-%d.mkv", index+1))
		if err := os.WriteFile(videoPath, []byte("synthetic video"), 0o600); err != nil {
			t.Fatalf("write video: %v", err)
		}
		mediaInfoPath := filepath.Join(root, "mediainfo.json")
		if err := os.WriteFile(mediaInfoPath, mediaInfoPayload, 0o600); err != nil {
			t.Fatalf("write mediainfo: %v", err)
		}
		discs = append(discs, api.ScreenshotDiscSubject{
			ID:                discID,
			Name:              fmt.Sprintf("Disc %d", index+1),
			Root:              root,
			VideoPath:         videoPath,
			MediaInfoJSONPath: mediaInfoPath,
		})
		videos = append(videos, videoPath)
	}
	return api.ScreenshotSubject{SourcePath: collectionRoot, Discs: discs}, videos[0], videos[1]
}

func writeTestBundledFFmpeg(root string) error {
	folder := osFolder()
	if folder == "" {
		return nil
	}
	name := "ffmpeg"
	if folder == "windows" {
		name = "ffmpeg.exe"
	}
	path := filepath.Join(root, "bin", "ffmpeg", folder, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir bundled ffmpeg dir: %w", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		return fmt.Errorf("write bundled ffmpeg: %w", err)
	}
	return nil
}

func ffmpegInputArg(args []string) string {
	return ffmpegValueAfter(args, "-i")
}

func ffmpegValueAfter(args []string, key string) string {
	for idx := 0; idx+1 < len(args); idx++ {
		if args[idx] == key {
			return args[idx+1]
		}
	}
	return ""
}

func TestPlanResuggestsDeletedAndStaleScreenshotSlots(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	mediaInfoPath := filepath.Join(t.TempDir(), "mediainfo.json")
	payload := []byte(`{"media":{"track":[{"@type":"General","Duration":"600"},{"@type":"Video","FrameRate":"24.000"}]}}`)
	if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	repo := openScreenshotTestRepository(t)
	tmpRoot := t.TempDir()
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
	meta := api.ScreenshotSubject{
		MediaBinding:      screenshotTestBinding(sourcePath),
		SourcePath:        sourcePath,
		MediaInfoJSONPath: mediaInfoPath,
	}

	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	base := screenshotBaseName(meta)
	suggestedIndices := func(plan api.ScreenshotPlan) []int {
		indices := make([]int, 0, len(plan.SuggestedSelections))
		for _, selection := range plan.SuggestedSelections {
			indices = append(indices, selection.Index)
		}
		return indices
	}

	capturePath := filepath.Join(tmpDir, buildScreenshotFilename(base, 0, 30, api.ScreenshotPurposeFinal))
	if err := os.WriteFile(capturePath, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	if err := repo.SaveScreenshot(context.Background(), meta.MediaBinding, api.Screenshot{
		SourcePath: meta.SourcePath,
		ImagePath:  capturePath,
		Timestamp:  30,
		Purpose:    api.ScreenshotPurposeFinal,
		CapturedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save screenshot: %v", err)
	}
	occupied, err := service.Plan(context.Background(), meta, 4)
	if err != nil {
		t.Fatalf("plan with capture: %v", err)
	}
	if got := suggestedIndices(occupied); len(got) != 3 || slices.Contains(got, 0) {
		t.Fatalf("occupied slot was resuggested: %v", got)
	}

	if err := service.Delete(context.Background(), meta, capturePath); err != nil {
		t.Fatalf("delete capture: %v", err)
	}
	if _, err := os.Stat(capturePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capture file survived delete: %v", err)
	}
	freed, err := service.Plan(context.Background(), meta, 4)
	if err != nil {
		t.Fatalf("plan after delete: %v", err)
	}
	if got := suggestedIndices(freed); len(got) != 4 || !slices.Contains(got, 0) {
		t.Fatalf("deleted slot was not resuggested: %v", got)
	}

	stalePath := filepath.Join(tmpDir, buildScreenshotFilename(base, 1, 157.5, api.ScreenshotPurposeFinal))
	if err := repo.SaveScreenshot(context.Background(), meta.MediaBinding, api.Screenshot{
		SourcePath: meta.SourcePath,
		ImagePath:  stalePath,
		Timestamp:  157.5,
		Purpose:    api.ScreenshotPurposeFinal,
		CapturedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save stale screenshot: %v", err)
	}
	stale, err := service.Plan(context.Background(), meta, 4)
	if err != nil {
		t.Fatalf("plan with stale row: %v", err)
	}
	if got := suggestedIndices(stale); len(got) != 4 || !slices.Contains(got, 1) {
		t.Fatalf("stale row blocked its slot: %v", got)
	}
}

func TestDeleteRejectsPathsOutsideManagedTempDir(t *testing.T) {
	tmpRoot := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	service := NewService(config.Config{}, api.NopLogger{}, tmpRoot, nil)
	meta := api.ScreenshotSubject{MediaBinding: screenshotTestBinding(sourcePath), SourcePath: sourcePath}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	siblingDir := tmpDir + "-evil"
	if err := os.MkdirAll(siblingDir, 0o700); err != nil {
		t.Fatalf("create sibling dir: %v", err)
	}
	siblingTarget := filepath.Join(siblingDir, "escape.png")
	if err := os.WriteFile(siblingTarget, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write sibling image: %v", err)
	}
	traversalTarget := tmpDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "escape.png"
	for _, testCase := range []struct {
		name   string
		target string
	}{
		{name: "traversal", target: traversalTarget},
		{name: "sibling prefix", target: siblingTarget},
		{name: "disallowed extension", target: filepath.Join(tmpDir, "notes.txt")},
	} {
		if err := service.Delete(context.Background(), meta, testCase.target); !errors.Is(err, internalerrors.ErrInvalidInput) {
			t.Fatalf("%s delete error = %v, want invalid input", testCase.name, err)
		}
	}
	if _, err := os.Stat(siblingTarget); err != nil {
		t.Fatalf("sibling image was removed: %v", err)
	}

	validTarget := filepath.Join(tmpDir, "capture.png")
	if err := os.WriteFile(validTarget, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write managed image: %v", err)
	}
	if err := service.Delete(context.Background(), meta, validTarget); err != nil {
		t.Fatalf("delete managed image: %v", err)
	}
	if _, err := os.Stat(validTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed image survived delete: %v", err)
	}
}

func TestDeleteAcceptsWindowsCaseAndSeparatorVariants(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path semantics")
	}
	tmpRoot := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	repo := openScreenshotTestRepository(t)
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
	meta := api.ScreenshotSubject{MediaBinding: screenshotTestBinding(sourcePath), SourcePath: sourcePath}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	retained := filepath.Join(tmpDir, "keep.png")
	for _, testCase := range []struct {
		name    string
		variant func(string) string
	}{
		{name: "upper case", variant: strings.ToUpper},
		{name: "forward slashes", variant: filepath.ToSlash},
	} {
		target := filepath.Join(tmpDir, "capture.png")
		if err := os.WriteFile(target, []byte("synthetic image"), 0o600); err != nil {
			t.Fatalf("write managed image: %v", err)
		}
		seedStoredCaptures(t, repo, meta.SourcePath, target, retained)
		if err := service.Delete(context.Background(), meta, testCase.variant(target)); err != nil {
			t.Fatalf("%s delete error = %v", testCase.name, err)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s variant did not remove managed image: %v", testCase.name, err)
		}
		requireStoredCaptureRows(t, repo, meta.SourcePath, retained, testCase.name)
	}
}

func TestDeleteAcceptsDarwinCaseVariant(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin path semantics")
	}
	tmpRoot := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	repo := openScreenshotTestRepository(t)
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
	meta := api.ScreenshotSubject{MediaBinding: screenshotTestBinding(sourcePath), SourcePath: sourcePath}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	target := filepath.Join(tmpDir, "capture.png")
	if err := os.WriteFile(target, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write managed image: %v", err)
	}
	caseVariant := filepath.Join(tmpDir, "CAPTURE.PNG")
	if _, err := os.Stat(caseVariant); errors.Is(err, os.ErrNotExist) {
		t.Skip("case-sensitive filesystem")
	} else if err != nil {
		t.Fatalf("inspect case variant: %v", err)
	}
	retained := filepath.Join(tmpDir, "keep.png")
	seedStoredCaptures(t, repo, meta.SourcePath, target, retained)

	if err := service.Delete(context.Background(), meta, caseVariant); err != nil {
		t.Fatalf("delete case variant: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("case variant did not remove managed image: %v", err)
	}
	requireStoredCaptureRows(t, repo, meta.SourcePath, retained, "darwin case variant")
}

// seedStoredCaptures stores the screenshot, hosted-upload, and final-selection
// rows the given captures own, each keyed on its canonical path spelling.
// Selections are written in one call because saving them replaces the release's
// whole set.
func seedStoredCaptures(t *testing.T, repo *db.SQLiteRepository, sourcePath string, imagePaths ...string) {
	t.Helper()

	now := time.Now().UTC()
	binding := screenshotTestBinding(sourcePath)
	selections := make([]api.ScreenshotFinalSelection, 0, len(imagePaths))
	uploads := make([]api.UploadedImageLink, 0, len(imagePaths))
	for order, imagePath := range imagePaths {
		if err := repo.SaveScreenshot(context.Background(), binding, api.Screenshot{
			SourcePath: sourcePath,
			ImagePath:  imagePath,
			Timestamp:  float64(30 * (order + 1)),
			Purpose:    api.ScreenshotPurposeFinal,
			CapturedAt: now,
		}); err != nil {
			t.Fatalf("seed screenshot record: %v", err)
		}
		uploads = append(uploads, api.UploadedImageLink{
			SourcePath: sourcePath,
			ImagePath:  imagePath,
			Host:       "example-host",
			UsageScope: "global",
			RawURL:     "https://example.invalid/capture.png",
			UploadedAt: now,
		})
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: sourcePath,
			ImagePath:  imagePath,
			Order:      order,
			Source:     "generated",
			SelectedAt: now,
		})
	}
	if err := repo.SaveUploadedImages(context.Background(), binding, "example-host", uploads); err != nil {
		t.Fatalf("seed uploaded image records: %v", err)
	}
	if err := repo.SaveFinalSelections(context.Background(), binding, selections); err != nil {
		t.Fatalf("seed final selections: %v", err)
	}
}

// requireStoredCaptureRows fails when a delete left a stale row that a later
// regeneration at the same canonical path could reuse, or when resolving the
// caller's path to a stored spelling reached another capture's rows. Exactly one
// screenshot, upload, and selection row must survive, all naming retained.
func requireStoredCaptureRows(t *testing.T, repo *db.SQLiteRepository, sourcePath string, retained string, label string) {
	t.Helper()

	stored := storedCaptureRows(t, repo, sourcePath)
	if len(stored) != 3 {
		t.Fatalf("%s surviving records = %#v, want one per table for %s", label, stored, retained)
	}
	for _, imagePath := range stored {
		if imagePath != retained {
			t.Fatalf("%s surviving record = %s, want %s", label, imagePath, retained)
		}
	}
}

// storedCaptureRows returns the image path of every screenshot, uploaded-image,
// and final-selection row stored for the release.
func storedCaptureRows(t *testing.T, repo *db.SQLiteRepository, sourcePath string) []string {
	t.Helper()

	binding := screenshotTestBinding(sourcePath)
	screenshots, err := repo.ListScreenshotsByPath(context.Background(), binding)
	if err != nil {
		t.Fatalf("list screenshot records: %v", err)
	}
	uploads, err := repo.ListUploadedImagesByPath(context.Background(), binding)
	if err != nil {
		t.Fatalf("list uploaded image records: %v", err)
	}
	selections, err := repo.ListFinalSelections(context.Background(), binding)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	stored := make([]string, 0, len(screenshots)+len(uploads)+len(selections))
	for _, record := range screenshots {
		stored = append(stored, record.ImagePath)
	}
	for _, upload := range uploads {
		stored = append(stored, upload.ImagePath)
	}
	for _, selection := range selections {
		stored = append(stored, selection.ImagePath)
	}
	return stored
}

// flakyDeleteRepository fails screenshot-record deletion until healthy is set, so
// a test can drive a persistent cleanup failure and then let a retry converge.
type flakyDeleteRepository struct {
	*db.SQLiteRepository
	healthy bool
}

func (r *flakyDeleteRepository) DeleteScreenshot(ctx context.Context, binding api.PreparedMediaBinding, imagePath string) error {
	if !r.healthy {
		return errors.New("synthetic screenshot record delete failure")
	}
	if err := r.SQLiteRepository.DeleteScreenshot(ctx, binding, imagePath); err != nil {
		return fmt.Errorf("delete screenshot: %w", err)
	}
	return nil
}

// TestDeleteReportsIncompleteCleanupAndConvergesOnRetry pins the delete contract
// once the file is gone. A caller must be able to tell a complete delete from one
// that left records behind — a silent success there is what lets a later capture
// at the same path reuse stale metadata — and calling Delete again has to
// converge, which it can only do because an already-missing file is not an error.
func TestDeleteReportsIncompleteCleanupAndConvergesOnRetry(t *testing.T) {
	tmpRoot := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	repo := &flakyDeleteRepository{SQLiteRepository: openScreenshotTestRepository(t)}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
	meta := api.ScreenshotSubject{MediaBinding: screenshotTestBinding(sourcePath), SourcePath: sourcePath}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	target := filepath.Join(tmpDir, "capture.png")
	if err := os.WriteFile(target, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write managed image: %v", err)
	}
	seedStoredCaptures(t, repo.SQLiteRepository, meta.SourcePath, target)

	err = service.Delete(context.Background(), meta, target)
	if err == nil || !strings.Contains(err.Error(), "delete cleanup incomplete") {
		t.Fatalf("delete error = %v, want a reported cleanup failure", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("reported cleanup failure did not remove the image: %v", statErr)
	}
	if stored := storedCaptureRows(t, repo.SQLiteRepository, meta.SourcePath); len(stored) == 0 {
		t.Fatal("cleanup reported a failure but left no stale record to retry")
	}

	repo.healthy = true
	if err := service.Delete(context.Background(), meta, target); err != nil {
		t.Fatalf("retried delete of an already-removed image: %v", err)
	}
	if stored := storedCaptureRows(t, repo.SQLiteRepository, meta.SourcePath); len(stored) != 0 {
		t.Fatalf("retry did not converge, surviving records = %#v", stored)
	}
}

func openScreenshotTestRepository(t *testing.T) *db.SQLiteRepository {
	t.Helper()

	repo, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	return repo
}

func screenshotTestBinding(sourcePath string) api.PreparedMediaBinding {
	return api.PreparedMediaBinding{
		SourcePath:               sourcePath,
		PreparedMediaFingerprint: "test-prepared-media",
		PreparedGeneration:       1,
	}
}
