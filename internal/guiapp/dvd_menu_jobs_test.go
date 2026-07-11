// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"strings"
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

func TestRunDVDMenuCaptureJobRedactsFailureMessage(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{
		dvdMenuCapture: func(context.Context, api.Request) (api.DVDMenuCaptureResult, error) {
			return api.DVDMenuCaptureResult{}, errors.New("provider rejected api_key=secret-value")
		},
	}
	job := &dvdMenuCaptureJob{id: "dvd-job-1", core: coreSvc, status: "queued", startedAt: time.Now().UTC()}

	(&App{}).runDVDMenuCaptureJob(context.Background(), nil, job)
	snapshot := buildDVDMenuCaptureSnapshot(job)
	if snapshot.Status != "failed" {
		t.Fatal("expected failed snapshot")
	}
	if strings.Contains(snapshot.Error, "secret-value") || !strings.Contains(snapshot.Error, "[REDACTED]") {
		t.Fatal("expected redacted DVD menu capture error")
	}
}

func TestPruneCompletedDVDMenuJobsKeepsNewestBoundedSet(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	oldest := &dvdMenuCaptureJob{
		id:             "oldest",
		status:         "completed",
		finishedAt:     now.Add(-3 * time.Minute),
		retentionTimer: time.NewTimer(time.Hour),
	}
	middle := &dvdMenuCaptureJob{id: "middle", status: "failed", finishedAt: now.Add(-2 * time.Minute)}
	newest := &dvdMenuCaptureJob{id: "newest", status: "canceled", finishedAt: now.Add(-time.Minute)}
	running := &dvdMenuCaptureJob{id: "running", status: "running", startedAt: now}
	app := &App{dvdMenus: map[string]*dvdMenuCaptureJob{
		oldest.id:  oldest,
		middle.id:  middle,
		newest.id:  newest,
		running.id: running,
	}}

	app.dvdMenuMu.Lock()
	app.pruneCompletedDVDMenuJobsLocked(2)
	app.dvdMenuMu.Unlock()

	if app.getDVDMenuCaptureJob(oldest.id) != nil {
		t.Fatal("expected oldest terminal DVD menu job to be evicted")
	}
	oldest.mu.Lock()
	oldestTimer := oldest.retentionTimer
	oldest.mu.Unlock()
	if oldestTimer != nil {
		t.Fatal("expected evicted DVD menu job retention timer to be released")
	}
	for _, jobID := range []string{middle.id, newest.id, running.id} {
		if app.getDVDMenuCaptureJob(jobID) == nil {
			t.Fatal("expected retained DVD menu job")
		}
	}
}
