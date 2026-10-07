// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeDupeProgressSeparatesTrackerAndWorkflowCounts(t *testing.T) {
	bridge := newCLITUIBridge()
	streams := cliIO{
		out: &cliPresentationWriter{
			Writer:    io.Discard,
			presenter: bridge,
			ctx:       t.Context(),
			terminal:  true,
		},
		presenter: bridge,
	}
	ctx := withCLIItemPresentation(t.Context(), streams, "Synthetic.Film.2026-GRP", 1)
	current := releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "dupe-progress", Revision: 3},
		Operation: &api.WorkflowOperationStatus{
			ID:         "dupe-operation",
			WorkflowID: "dupe-progress",
			Phase:      "check-duplicates",
			Status:     api.StageStatusRunning,
			Completed:  300,
			Total:      1000,
		},
	}
	core := &cliWorkflowCoreFake{startProgress: []api.DupeProgressUpdate{
		{
			Tracker:   "ALPHA",
			Status:    "completed",
			Completed: 16,
			Total:     17,
		},
		{
			Tracker: "BETA",
			Status:  "running",
			Total:   17,
		},
	}}
	session := &cliWorkflowSession{
		core:          core,
		streams:       streams,
		current:       current,
		uploadRequest: api.Request{SourcePath: "Synthetic.Film.2026-GRP"},
	}
	session.publishPresentation(ctx)
	core.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
		model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		model.Update(bridge.receive(t.Context())())
		frame := ansi.Strip(model.flow(90, 20))
		for _, expected := range []string{"Duplicate checks: 16/17 trackers", "94%", "Overall workflow progress", "30%"} {
			if !strings.Contains(frame, expected) {
				t.Fatalf("missing %q in running progress: %s", expected, frame)
			}
		}
		if model.view.Completed != 300 || model.view.Total != 1000 || bridge.progress != nil {
			t.Fatal("tracker counter overwrote overall workflow progress")
		}
		model.view.Stage = "capture-media / running"
		if strings.Contains(ansi.Strip(model.flow(90, 20)), "Duplicate checks:") {
			t.Fatal("finished duplicate counter appeared in a later stage")
		}
		current.Operation = nil
		current.DryRun = &api.UploadDryRunResult{}
		return current, nil
	}
	if _, err := session.completeComposite(ctx, true, nil, config.Config{}, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
}

func TestDupeProgressRejectsPreviousPresentationEpoch(t *testing.T) {
	bridge := newCLITUIBridge()
	session := &cliWorkflowSession{streams: cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}}}
	previous := session.presentationProgressContext(t.Context())
	current := session.presentationProgressContext(t.Context())
	api.EmitDupeProgress(current, api.DupeProgressUpdate{Completed: 1, Total: 3})
	api.EmitDupeProgress(previous, api.DupeProgressUpdate{Completed: 17, Total: 17})
	if len(bridge.telemetry) != 1 || bridge.telemetry[0].Completed != 1 || bridge.telemetry[0].Total != 3 {
		t.Fatal("an earlier duplicate check replaced current stage progress")
	}
	api.EmitDupeProgress(current, api.DupeProgressUpdate{Status: "queued", Total: 2})
	if len(bridge.telemetry) != 1 || bridge.telemetry[0].Completed != 0 || bridge.telemetry[0].Total != 2 {
		t.Fatal("a restarted duplicate check retained previous completion counts")
	}
}
