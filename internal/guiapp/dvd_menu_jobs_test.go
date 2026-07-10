// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRunDVDMenuCaptureJobPublishesProgressAndResult(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{
		dvdMenuCapture: func(ctx context.Context, _ api.Request) (api.DVDMenuCaptureResult, error) {
			api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{
				Phase:           "capturing",
				Message:         "Rendering distinct DVD menu screens.",
				DiscoveredMenus: 3,
				VisitedStates:   5,
				VisitedButtons:  4,
				CapturedCount:   1,
				WarningCount:    1,
			})
			return api.DVDMenuCaptureResult{
				SourcePath:      "Example.Release.2026.1080p-GRP",
				DiscoveredMenus: 3,
				VisitedStates:   5,
				VisitedButtons:  4,
				Partial:         true,
				Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{
					Path:    "menu-1.png",
					Purpose: api.ScreenshotPurposeMenu,
				}}},
				Warnings: []api.DVDMenuCaptureWarning{{Code: "frame_decode", Message: "One menu could not be rendered."}},
			}, nil
		},
	}
	job := &dvdMenuCaptureJob{
		id:         "dvd-job-1",
		sourcePath: "Example.Release.2026.1080p-GRP",
		core:       coreSvc,
		status:     "queued",
		startedAt:  time.Now().UTC(),
	}

	(&App{}).runDVDMenuCaptureJob(context.Background(), nil, job)
	snapshot := buildDVDMenuCaptureSnapshot(job)
	if snapshot.Status != "completed" || snapshot.Phase != "complete" {
		t.Fatalf("unexpected terminal snapshot: %#v", snapshot)
	}
	if snapshot.CapturedCount != 1 || snapshot.WarningCount != 1 || !snapshot.Result.Partial {
		t.Fatalf("expected partial persisted result, got %#v", snapshot)
	}
	if got := coreSvc.dvdMenuCalls.Load(); got != 1 {
		t.Fatalf("expected one capture call, got %d", got)
	}
	if got := coreSvc.count.Load(); got != 1 {
		t.Fatalf("expected job core close, got %d", got)
	}
}

func TestRunDVDMenuCaptureJobClassifiesCancellation(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{
		dvdMenuCapture: func(ctx context.Context, _ api.Request) (api.DVDMenuCaptureResult, error) {
			return api.DVDMenuCaptureResult{}, ctx.Err()
		},
	}
	job := &dvdMenuCaptureJob{id: "dvd-job-1", core: coreSvc, status: "queued", startedAt: time.Now().UTC()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	(&App{}).runDVDMenuCaptureJob(ctx, nil, job)
	snapshot := buildDVDMenuCaptureSnapshot(job)
	if snapshot.Status != "canceled" || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("expected canceled snapshot, got %#v", snapshot)
	}
}
