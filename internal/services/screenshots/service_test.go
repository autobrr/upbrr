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
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
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
	}, 0.5)
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

type runnerCall struct {
	args []string
}

type scriptedRunner struct {
	results []CommandResult
	calls   []runnerCall
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
