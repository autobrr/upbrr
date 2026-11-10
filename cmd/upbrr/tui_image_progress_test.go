// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestImageHostProgressPreservesWorkflowCountsAndHostOutcomes(t *testing.T) {
	bridge := newCLITUIBridge()
	streams := cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge}
	ctx := withCLIItemPresentation(t.Context(), streams, "Example.Release.2026-GRP", 1)
	session := &cliWorkflowSession{streams: streams, current: releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "image-progress", Revision: 2},
		Operation: &api.WorkflowOperationStatus{
			ID:         "image-operation",
			WorkflowID: "image-progress",
			Phase:      "capture-media",
			Status:     api.StageStatusRunning,
			Completed:  300,
			Total:      1000,
		},
	}}
	session.publishPresentation(ctx)
	ctx = session.presentationProgressContext(ctx)
	for _, update := range []api.ImageUploadProgressUpdate{
		{
			AttemptID:  "pixhost|global",
			Host:       "pixhost",
			UsageScope: "global",
			Status:     api.ImageUploadProgressRunning,
			Total:      3,
			Message:    "Uploading images.",
		},
		{
			AttemptID:  "pixhost|global",
			Host:       "pixhost",
			UsageScope: "global",
			Status:     api.ImageUploadProgressFailed,
			Completed:  3,
			Total:      3,
			Succeeded:  2,
			Failed:     1,
			Message:    "One image unavailable.",
		},
		{
			AttemptID:  "imgbb|global",
			Host:       "imgbb",
			UsageScope: "global",
			Status:     api.ImageUploadProgressCompleted,
			Completed:  3,
			Total:      3,
			Reused:     3,
			Fallback:   true,
			Message:    "Reused existing host uploads. token=synthetic-secret\x1b[2J",
		},
	} {
		api.EmitImageUploadProgress(ctx, update)
	}
	model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
	model.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	model.Update(bridge.drain())
	if model.view.Completed != 300 || model.view.Total != 1000 || model.view.Stage != "capture-media / running" || bridge.progress != nil {
		t.Fatal("image attempt replaced the workflow's stage or aggregate counts")
	}
	frame := ansi.Strip(model.flow(160, 40))
	for _, want := range []string{"pixhost", "One image unavailable.", "2 uploaded, 0 reused, 1 failed", "imgbb (fallback)", "0 uploaded, 3 reused, 0 failed", "Reused existing host uploads.", "Overall workflow progress", "30%"} {
		if !strings.Contains(frame, want) {
			t.Errorf("image progress lost %q", want)
		}
	}
	if len(model.telemetry) != 2 || strings.Contains(frame, "Uploading images.") || strings.Contains(frame, "synthetic-secret") {
		t.Fatal("host progress was not coalesced or sanitized")
	}

	api.EmitImageUploadProgress(ctx, api.ImageUploadProgressUpdate{
		AttemptID:  "ptscreens|global",
		Host:       "ptscreens",
		UsageScope: "global",
		Status:     api.ImageUploadProgressCompleted,
		Completed:  3,
		Total:      3,
		Succeeded:  3,
		Fallback:   true,
		Message:    "Latest host ready.",
	})
	model.Update(bridge.drain())
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	frame = ansi.Strip(model.View().Content)
	for _, want := range []string{"ptscreens", "Latest host ready.", "Overall workflow progress", "30%"} {
		if !strings.Contains(frame, want) {
			t.Errorf("compact rendered view lost %q", want)
		}
	}
	model.Update(modelKey(tea.KeyPgUp))
	frame = ansi.Strip(model.View().Content)
	if !strings.Contains(frame, "pixhost") || !strings.Contains(frame, "30%") {
		t.Errorf("earlier host details were not scrollable independently of workflow progress: %s", frame)
	}
	ctx = session.presentationProgressContext(ctx)
	api.EmitImageUploadProgress(ctx, api.ImageUploadProgressUpdate{
		AttemptID:  "imgbb|global",
		Host:       "imgbb",
		UsageScope: "global",
		Status:     api.ImageUploadProgressRunning,
		Total:      3,
		Message:    "New attempt.",
	})
	model.Update(bridge.drain())
	frame = ansi.Strip(model.View().Content)
	if !strings.Contains(frame, "New attempt") || strings.Contains(frame, "pixhost") || strings.Contains(frame, "ptscreens") {
		t.Errorf("new reporter epoch retained previous host details or scroll position: %s", frame)
	}
}
