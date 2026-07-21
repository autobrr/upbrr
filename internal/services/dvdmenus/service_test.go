// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvdmenus

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/dvdvideo/engine"
	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/render"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type capabilityRunner struct {
	calls int
}

type dvdMenuRecordingLogger struct {
	entries []string
}

func (l *dvdMenuRecordingLogger) add(level, format string, args ...any) {
	l.entries = append(l.entries, level+" "+fmt.Sprintf(format, args...))
}

func (l *dvdMenuRecordingLogger) Tracef(format string, args ...any) {
	l.add("TRACE", format, args...)
}

func (l *dvdMenuRecordingLogger) Debugf(format string, args ...any) {
	l.add("DEBUG", format, args...)
}

func (l *dvdMenuRecordingLogger) Infof(format string, args ...any) {
	l.add("INFO", format, args...)
}

func (l *dvdMenuRecordingLogger) Warnf(format string, args ...any) {
	l.add("WARN", format, args...)
}

func (l *dvdMenuRecordingLogger) Errorf(format string, args ...any) {
	l.add("ERROR", format, args...)
}

func (l *dvdMenuRecordingLogger) Contains(expected string) bool {
	for _, entry := range l.entries {
		if strings.Contains(entry, expected) {
			return true
		}
	}
	return false
}

func (l *dvdMenuRecordingLogger) Count(expected string) int {
	count := 0
	for _, entry := range l.entries {
		if strings.Contains(entry, expected) {
			count++
		}
	}
	return count
}

func (r *capabilityRunner) Run(_ context.Context, _ string, args []string, _ int) (render.Output, error) {
	r.calls++
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "demuxer=dvdvideo") {
		return render.Output{Stdout: []byte("dvdvideo -menu -menu_lu -menu_vts -pgc -pg")}, nil
	}
	if strings.Contains(joined, "-version") {
		return render.Output{Stdout: []byte("ffmpeg version example\n")}, nil
	}
	return render.Output{}, errors.New("unexpected FFmpeg call")
}

func TestCaptureRerunListAndDeletePreserveManualMenus(t *testing.T) {
	t.Parallel()

	repo := openTestRepository(t)
	tmpRoot := t.TempDir()
	discRoot := t.TempDir()
	videoTS := filepath.Join(discRoot, "VIDEO_TS")
	if err := os.Mkdir(videoTS, 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "ffmpeg-example")
	if err := os.WriteFile(executable, []byte("example"), 0o600); err != nil {
		t.Fatalf("write FFmpeg identity: %v", err)
	}

	meta := api.DVDMenuSubject{SourcePath: discRoot, DiscType: "DVD"}
	managedDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, api.ReleaseInfo{})
	if err != nil {
		t.Fatalf("managed dir: %v", err)
	}
	manualPath := filepath.Join(managedDir, "manual-dvd-menu-example.png")
	writePNG(t, manualPath, color.NRGBA{
		R: 20,
		G: 40,
		B: 60,
		A: 255,
	})
	now := time.Now().UTC()
	if err := repo.AppendManualMenuScreenshots(context.Background(), meta.SourcePath,
		[]api.Screenshot{{
			SourcePath: meta.SourcePath,
			ImagePath:  manualPath,
			Width:      2,
			Height:     2,
			Purpose:    api.ScreenshotPurposeMenu,
			CapturedAt: now,
		}},
		[]api.ScreenshotFinalSelection{{
			SourcePath: meta.SourcePath,
			ImagePath:  manualPath,
			Source:     api.ScreenshotSelectionSourceMenu,
			SelectedAt: now,
		}},
	); err != nil {
		t.Fatalf("seed manual menu: %v", err)
	}

	captures := []engine.Capture{
		{Image: solidImage(color.NRGBA{
			R: 200,
			G: 10,
			B: 10,
			A: 255,
		}), Discovery: graph.DiscoveryReachable},
		{Image: solidImage(color.NRGBA{
			R: 10,
			G: 200,
			B: 10,
			A: 255,
		}), Discovery: graph.DiscoveryStructural},
	}
	progressUpdates := make([]api.DVDMenuProgressUpdate, 0)
	progressCtx := api.WithDVDMenuProgressReporter(context.Background(), func(update api.DVDMenuProgressUpdate) {
		progressUpdates = append(progressUpdates, update)
	})
	runner := &capabilityRunner{}
	logger := &dvdMenuRecordingLogger{}
	service := newService(logger, tmpRoot, repo, runner, func() (string, error) { return executable, nil },
		func(_ context.Context, root string, _ render.Runner, _ string, options engine.Options) (engine.Result, error) {
			rootInfo, err := os.Stat(root)
			if err != nil {
				t.Fatal("capture root is unavailable")
			}
			videoTSInfo, err := os.Stat(videoTS)
			if err != nil {
				t.Fatal("expected VIDEO_TS directory is unavailable")
			}
			if !os.SameFile(rootInfo, videoTSInfo) {
				t.Fatal("capture root does not identify the expected VIDEO_TS directory")
			}
			if options.Capability == nil || !options.Capability.Available {
				t.Fatal("capture did not receive cached capability")
			}
			if options.Logger == nil {
				t.Fatal("capture did not receive logger")
			}
			if options.Progress != nil {
				options.Progress(engine.Progress{
					Phase:          "capturing",
					Inventoried:    len(captures),
					VisitedStates:  4,
					VisitedButtons: 3,
					Captured:       len(captures),
				})
			}
			return engine.Result{
				EngineVersion:  engine.Version,
				Captures:       append([]engine.Capture(nil), captures...),
				Inventoried:    len(captures),
				Selected:       len(captures),
				VisitedStates:  4,
				VisitedButtons: 3,
				Partial:        true,
				Warnings: []graph.Warning{{
					Code:    "unsupported_pre_link",
					Message: "menu pre-command target was not resolved",
				}},
			}, nil
		})

	first, err := service.Capture(progressCtx, meta, 6)
	if err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if len(first.Images) != 2 || first.Images[0].Purpose != api.ScreenshotPurposeMenu || first.Images[1].Discovery != api.DVDMenuDiscoveryStructural {
		t.Fatalf("first capture result = %#v", first)
	}
	if !hasWarning(first.Warnings, "structural_discovery") {
		t.Fatalf("missing structural warning: %#v", first.Warnings)
	}
	for _, expected := range []string{
		"DEBUG DVD menus: capture requested disc_type=DVD max_items=6",
		"DEBUG DVD menus: FFmpeg capability probe complete dvdvideo=true options=5",
		"INFO DVD menus: capture started language=en region=0",
		"DEBUG DVD menus: progress phase=capturing",
		`DEBUG DVD menus: warning recorded code=unsupported_pre_link detail="menu pre-command target was not resolved"`,
		"DEBUG DVD menus: persistence started images=2",
		"DEBUG DVD menus: persistence complete stored=2",
		"WARN DVD menus: capture incomplete captured=2",
		"INFO DVD menus: capture complete captured=2 discovered=2 states=4 buttons=3 complete=false",
	} {
		if !logger.Contains(expected) {
			t.Fatalf("missing DVD menu service log event %q", expected)
		}
	}
	if logger.Contains(discRoot) || logger.Contains(executable) {
		t.Fatal("DVD menu service logs exposed a local path")
	}
	if got := logger.Count("INFO DVD menus:"); got != 2 {
		t.Fatalf("DVD menu capture INFO logs = %d, want start and completion only", got)
	}
	assertProgressPhases(t, progressUpdates, []string{"preflight", "capturing", "persisting", "complete"})
	firstAutoPaths := []string{first.Images[0].Path, first.Images[1].Path}
	assertSelectionSources(t, repo, meta.SourcePath, []string{
		api.ScreenshotSelectionSourceDVDMenu,
		api.ScreenshotSelectionSourceDVDMenu,
		api.ScreenshotSelectionSourceMenu,
	})

	captures = captures[:1]
	second, err := service.Capture(context.Background(), meta, 6)
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if len(second.Images) != 1 {
		t.Fatalf("second images = %d, want 1", len(second.Images))
	}
	for _, oldPath := range firstAutoPaths {
		if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("replaced capture still exists: %v", err)
		}
	}
	if _, err := os.Stat(manualPath); err != nil {
		t.Fatalf("manual image removed by rerun: %v", err)
	}

	listed, err := service.List(context.Background(), meta)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 || listed[0].Path != second.Images[0].Path || listed[1].Path != manualPath {
		t.Fatalf("listed images = %#v", listed)
	}
	for _, listedImage := range listed {
		if listedImage.Purpose != api.ScreenshotPurposeMenu {
			t.Fatalf("listed purpose = %q", listedImage.Purpose)
		}
	}

	if err := repo.SaveUploadedImages(context.Background(), meta.SourcePath, "example-host", []api.UploadedImageLink{{
		SourcePath: meta.SourcePath,
		ImagePath:  second.Images[0].Path,
		Host:       "example-host",
		UsageScope: "global",
		RawURL:     "https://example.invalid/dvd-menu.png",
		UploadedAt: now,
	}}); err != nil {
		t.Fatalf("save upload link: %v", err)
	}
	if err := service.Delete(context.Background(), meta, second.Images[0].Path); err != nil {
		t.Fatalf("delete automatic menu: %v", err)
	}
	if _, err := os.Stat(second.Images[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted image stat = %v", err)
	}
	assertSelectionSources(t, repo, meta.SourcePath, []string{api.ScreenshotSelectionSourceMenu})
}

func assertProgressPhases(t *testing.T, updates []api.DVDMenuProgressUpdate, want []string) {
	t.Helper()
	got := make([]string, 0, len(updates))
	for _, update := range updates {
		got = append(got, update.Phase)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("progress phases = %#v, want %#v", got, want)
	}
}

func TestCaptureRollsBackCreatedFilesWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	baseRepo := openTestRepository(t)
	repo := &failingCaptureRepository{SQLiteRepository: baseRepo}
	tmpRoot := t.TempDir()
	discRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(discRoot, "VIDEO_TS"), 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "ffmpeg-example")
	if err := os.WriteFile(executable, []byte("example"), 0o600); err != nil {
		t.Fatalf("write FFmpeg identity: %v", err)
	}
	service := newService(api.NopLogger{}, tmpRoot, repo, &capabilityRunner{}, func() (string, error) { return executable, nil },
		func(context.Context, string, render.Runner, string, engine.Options) (engine.Result, error) {
			return engine.Result{Captures: []engine.Capture{{Image: solidImage(color.NRGBA{R: 50, A: 255}), Discovery: graph.DiscoveryReachable}}}, nil
		})

	_, err := service.Capture(context.Background(), api.DVDMenuSubject{SourcePath: discRoot, DiscType: "DVD"}, 1)
	if err == nil || !strings.Contains(err.Error(), "persist capture") {
		t.Fatalf("capture error = %v", err)
	}
	managedDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{}, discRoot)
	if err != nil {
		t.Fatalf("managed dir: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(managedDir, "*-dvd-menu-*.png"))
	if err != nil {
		t.Fatalf("glob captures: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("rollback retained captures: %#v", matches)
	}
}

func TestDeleteRejectsOriginalOutsideManagedDirectory(t *testing.T) {
	t.Parallel()

	repo := openTestRepository(t)
	tmpRoot := t.TempDir()
	discRoot := t.TempDir()
	original := filepath.Join(t.TempDir(), "original-menu.png")
	writePNG(t, original, color.NRGBA{B: 200, A: 255})
	if err := repo.SaveFinalSelections(context.Background(), discRoot, []api.ScreenshotFinalSelection{{
		SourcePath: discRoot,
		ImagePath:  original,
		Source:     api.ScreenshotSelectionSourceMenu,
		SelectedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("save original selection: %v", err)
	}
	service := NewService(api.NopLogger{}, tmpRoot, repo)
	err := service.Delete(context.Background(), api.DVDMenuSubject{SourcePath: discRoot, DiscType: "DVD"}, original)
	if err == nil || !strings.Contains(err.Error(), "outside the managed release directory") {
		t.Fatalf("delete error = %v", err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("original image changed: %v", err)
	}
}

func TestDeleteRestoresFileAndRecordsWhenFinalRemoveFails(t *testing.T) {
	t.Parallel()

	repo := openTestRepository(t)
	tmpRoot := t.TempDir()
	discRoot := t.TempDir()
	meta := api.DVDMenuSubject{SourcePath: discRoot, DiscType: "DVD"}
	managedDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, api.ReleaseInfo{})
	if err != nil {
		t.Fatal("create managed directory failed")
	}
	imagePath := filepath.Join(managedDir, "Example.Release.2026-dvd-menu-01.png")
	writePNG(t, imagePath, color.NRGBA{
		R: 20,
		G: 40,
		B: 60,
		A: 255,
	})
	now := time.Now().UTC()
	if err := repo.AppendManualMenuScreenshots(context.Background(), meta.SourcePath,
		[]api.Screenshot{{
			SourcePath: meta.SourcePath,
			ImagePath:  imagePath,
			Width:      2,
			Height:     2,
			Purpose:    api.ScreenshotPurposeMenu,
			CapturedAt: now,
		}},
		[]api.ScreenshotFinalSelection{{
			SourcePath: meta.SourcePath,
			ImagePath:  imagePath,
			Source:     api.ScreenshotSelectionSourceMenu,
			SelectedAt: now,
		}},
	); err != nil {
		t.Fatal("seed menu screenshot failed")
	}
	if err := repo.SaveUploadedImages(context.Background(), meta.SourcePath, "example-host", []api.UploadedImageLink{{
		SourcePath: meta.SourcePath,
		ImagePath:  imagePath,
		Host:       "example-host",
		UsageScope: "global",
		RawURL:     "https://example.invalid/dvd-menu.png",
		UploadedAt: now,
	}}); err != nil {
		t.Fatal("seed menu upload record failed")
	}
	if err := repo.ReplaceScreenshotSlots(context.Background(), meta.SourcePath, []api.ScreenshotSlot{{
		SourcePath: meta.SourcePath,
		SlotOrder:  0,
		ImagePath:  imagePath,
		Variants: []api.ScreenshotSlotVariant{{
			SourcePath: meta.SourcePath,
			SlotOrder:  0,
			Host:       "example-host",
			UsageScope: "global",
			ImagePath:  imagePath,
			RawURL:     "https://example.invalid/dvd-menu.png",
			UploadedAt: now,
		}},
	}}); err != nil {
		t.Fatal("seed menu screenshot slot failed")
	}

	service := NewService(api.NopLogger{}, tmpRoot, repo)
	service.removeFile = func(string) error { return errors.New("synthetic remove failure") }
	err = service.Delete(context.Background(), meta, imagePath)
	if err == nil || !strings.Contains(err.Error(), "deletion rolled back") {
		t.Fatal("expected final remove failure to roll back deletion")
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatal("expected rolled-back image at its original path")
	}
	listed, err := service.List(context.Background(), meta)
	if err != nil {
		t.Fatal("list rolled-back menu image failed")
	}
	if len(listed) != 1 || listed[0].Path != imagePath {
		t.Fatal("rolled-back image is not recoverable through the menu list")
	}
	uploads, err := repo.ListUploadedImagesByPath(context.Background(), meta.SourcePath)
	if err != nil {
		t.Fatal("list rolled-back upload association failed")
	}
	restoredUpload := false
	for _, uploaded := range uploads {
		if uploaded.ImagePath == imagePath {
			restoredUpload = true
			break
		}
	}
	if !restoredUpload {
		t.Fatal("rolled-back upload association was not restored")
	}
	slots, err := repo.ListScreenshotSlotsByPath(context.Background(), meta.SourcePath)
	if err != nil || len(slots) != 1 || slots[0].ImagePath != imagePath || len(slots[0].Variants) != 1 {
		t.Fatal("rolled-back screenshot slot associations were not restored")
	}
	pending, err := filepath.Glob(imagePath + ".delete-*")
	if err != nil || len(pending) != 0 {
		t.Fatal("rolled-back delete left a staged image behind")
	}
}

func TestCapabilityCacheInvalidatesWhenExecutableIdentityChanges(t *testing.T) {
	t.Parallel()

	repo := openTestRepository(t)
	executable := filepath.Join(t.TempDir(), "ffmpeg-example")
	if err := os.WriteFile(executable, []byte("first"), 0o600); err != nil {
		t.Fatalf("write FFmpeg identity: %v", err)
	}
	runner := &capabilityRunner{}
	service := newService(api.NopLogger{}, t.TempDir(), repo, runner, func() (string, error) { return executable, nil }, nil)

	for range 2 {
		info, err := service.Capability(context.Background())
		if err != nil {
			t.Fatalf("capability: %v", err)
		}
		if !info.FFmpegDVDVideo || info.FFmpegVersion != "ffmpeg version example" {
			t.Fatalf("capability info = %#v", info)
		}
	}
	if runner.calls != 2 {
		t.Fatalf("cached probe calls = %d, want 2", runner.calls)
	}
	if err := os.WriteFile(executable, []byte("second identity"), 0o600); err != nil {
		t.Fatalf("change FFmpeg identity: %v", err)
	}
	if _, err := service.Capability(context.Background()); err != nil {
		t.Fatalf("capability after identity change: %v", err)
	}
	if runner.calls != 4 {
		t.Fatalf("invalidated probe calls = %d, want 4", runner.calls)
	}
}

func TestMissingFFmpegOptionsReturnsEveryReportedOption(t *testing.T) {
	t.Parallel()

	missing := missingFFmpegOptions(errors.New("FFmpeg capability: missing -menu_lu, -menu_vts, -pgc, -pg"))
	if strings.Join(missing, ",") != "-menu_lu,-menu_vts,-pgc,-pg" {
		t.Fatalf("missing options = %#v", missing)
	}
}

type failingCaptureRepository struct {
	*db.SQLiteRepository
}

func (r *failingCaptureRepository) ReplaceDVDMenuScreenshots(context.Context, string, []api.Screenshot, []api.ScreenshotFinalSelection) ([]string, error) {
	return nil, errors.New("synthetic persistence failure")
}

func openTestRepository(t *testing.T) *db.SQLiteRepository {
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

func solidImage(fill color.NRGBA) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			result.SetNRGBA(x, y, fill)
		}
	}
	return result
}

func writePNG(t *testing.T, path string, fill color.NRGBA) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create PNG: %v", err)
	}
	if err := png.Encode(file, solidImage(fill)); err != nil {
		_ = file.Close()
		t.Fatalf("encode PNG: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close PNG: %v", err)
	}
}

func assertSelectionSources(t *testing.T, repo *db.SQLiteRepository, sourcePath string, expected []string) {
	t.Helper()
	selections, err := repo.ListFinalSelections(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("list final selections: %v", err)
	}
	if len(selections) != len(expected) {
		t.Fatalf("selection count = %d, want %d: %#v", len(selections), len(expected), selections)
	}
	for index, source := range expected {
		if selections[index].Source != source || selections[index].Order != index {
			t.Fatalf("selection[%d] = %#v, want source %q order %d", index, selections[index], source, index)
		}
	}
}

func hasWarning(warnings []api.DVDMenuCaptureWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}
