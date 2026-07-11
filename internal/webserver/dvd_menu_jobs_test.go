// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

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

	coreSvc := &preparedMetaTestCore{
		dvdMenuCapture: func(ctx context.Context, _ api.Request) (api.DVDMenuCaptureResult, error) {
			api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{
				Phase:           "capturing",
				DiscoveredMenus: 2,
				VisitedStates:   4,
				VisitedButtons:  3,
				CapturedCount:   1,
			})
			return api.DVDMenuCaptureResult{
				SourcePath:      "Example.Release.2026.1080p-GRP",
				DiscoveredMenus: 2,
				VisitedStates:   4,
				VisitedButtons:  3,
				Complete:        true,
				Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{
					Path:    "menu-1.png",
					Purpose: api.ScreenshotPurposeMenu,
				}}},
			}, nil
		},
	}
	job := &dvdMenuCaptureJob{
		sessionID: "session-a",
		id:        "dvd-job-1",
		core:      coreSvc,
		status:    "queued",
		startedAt: time.Now().UTC(),
	}
	backend := &Backend{dvdMenus: map[string]*dvdMenuCaptureJob{job.id: job}}
	backend.dvdMenuWG.Add(1)

	backend.runDVDMenuCaptureJob(context.Background(), job)
	snapshot := buildWebDVDMenuCaptureSnapshot(job)
	if snapshot.Status != "completed" || snapshot.CapturedCount != 1 || !snapshot.Result.Complete {
		t.Fatalf("unexpected terminal snapshot: %#v", snapshot)
	}
	if coreSvc.dvdMenuCalls != 1 {
		t.Fatalf("expected one capture call, got %d", coreSvc.dvdMenuCalls)
	}
	if coreSvc.closeCalls != 1 {
		t.Fatalf("expected one core close, got %d", coreSvc.closeCalls)
	}
}

func TestRunDVDMenuCaptureJobRedactsFailureMessage(t *testing.T) {
	t.Parallel()

	coreSvc := &preparedMetaTestCore{
		dvdMenuCapture: func(context.Context, api.Request) (api.DVDMenuCaptureResult, error) {
			return api.DVDMenuCaptureResult{}, errors.New("provider rejected api_key=secret-value")
		},
	}
	job := &dvdMenuCaptureJob{
		sessionID: "session-a",
		id:        "dvd-job-1",
		core:      coreSvc,
		status:    "queued",
		startedAt: time.Now().UTC(),
	}
	backend := &Backend{dvdMenus: map[string]*dvdMenuCaptureJob{job.id: job}}
	backend.dvdMenuWG.Add(1)

	backend.runDVDMenuCaptureJob(context.Background(), job)
	snapshot := buildWebDVDMenuCaptureSnapshot(job)
	if snapshot.Status != "failed" {
		t.Fatal("expected failed snapshot")
	}
	if strings.Contains(snapshot.Error, "secret-value") || !strings.Contains(snapshot.Error, "[REDACTED]") {
		t.Fatal("expected redacted DVD menu capture error")
	}
}

func TestDVDMenuCaptureJobAccessRequiresOwningSession(t *testing.T) {
	t.Parallel()

	job := &dvdMenuCaptureJob{
		sessionID: "session-a",
		id:        "dvd-job-1",
		status:    "running",
		startedAt: time.Now().UTC(),
	}
	backend := &Backend{dvdMenus: map[string]*dvdMenuCaptureJob{job.id: job}}

	if _, err := backend.GetDVDMenuCaptureSnapshot("session-b", job.id); err == nil || err.Error() != "DVD menu capture job not found" {
		t.Fatal("expected foreign session snapshot read to use the not-found privacy response")
	}
	if err := backend.CancelDVDMenuCapture("session-b", job.id); err == nil || err.Error() != "DVD menu capture job not found" {
		t.Fatal("expected foreign session cancellation to use the not-found privacy response")
	}
	if _, err := backend.GetDVDMenuCaptureSnapshot("session-b", "missing-job"); err == nil || err.Error() != "DVD menu capture job not found" {
		t.Fatal("expected unknown snapshot read to use the same not-found privacy response")
	}
	if err := backend.CancelDVDMenuCapture("session-b", "missing-job"); err == nil || err.Error() != "DVD menu capture job not found" {
		t.Fatal("expected unknown cancellation to use the same not-found privacy response")
	}
	if _, err := backend.GetDVDMenuCaptureSnapshot("session-a", job.id); err != nil {
		t.Fatalf("expected owning session snapshot read: %v", err)
	}
}
