// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAttachedAudioArtifactsSurviveTerminalRestorationAndQueueAdvance(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			var output cliSynchronizedOutput
			artifactID := strings.Repeat("a", 32)
			status := api.StageStatusCompleted
			items := 2
			if partial {
				status, items = api.StageStatusPartial, 1
			}
			err := runCLIPresentation(t.Context(), cliIO{
				in:     strings.NewReader(""),
				out:    &output,
				errOut: io.Discard,
			},
				cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, false,
				func(ctx context.Context, _, _ []string, streams cliIO) error {
					for range items {
						itemCtx := withCLIItemPresentation(ctx, streams, "Synthetic.Film.2026.mkv", items)
						coreSvc := &cliWorkflowCoreFake{
							current: cliAudioAnalysisCurrentForTest(),
							audioResult: &api.AudioAnalysisResult{
								ID:     "analysis",
								Status: status,
								Tracks: []api.AudioAnalysisTrackResult{{
									Ordinal: 1,
									Status:  status,
									Artifacts: []api.AudioAnalysisArtifact{{
										ID:      api.PublicResourceID(artifactID),
										Variant: api.AudioAnalysisWaveform,
										Status:  api.StageStatusCompleted,
									}},
								}},
							},
						}
						session := &cliWorkflowSession{
							core:          coreSvc,
							current:       coreSvc.current,
							streams:       streams,
							uploadRequest: api.Request{Options: api.UploadOptions{AudioTracks: "primary", AudioImages: "waveform"}},
						}
						err := session.completeAudioAnalysis(itemCtx)
						publishCLIItemResult(streams, err)
						if err != nil {
							return err
						}
					}
					return nil
				})
			if (err != nil) != partial {
				t.Fatal("attached audio changed its terminal status")
			}
			text := output.String()
			line := "Audio analysis resource 1 track 1 waveform: " + filepath.Join("test-output", artifactID+".png")
			exit := strings.LastIndex(text, "\x1b[?1049l")
			if exit < 0 || strings.Contains(text[:exit], artifactID) || strings.Count(text[exit:], line) != items {
				t.Fatal("owner-authorized attached artifacts were lost, redacted, duplicated, or written before restoration")
			}
		})
	}
}

func TestDurableArtifactsBoundHistoryAndQuoteTerminalControls(t *testing.T) {
	bridge := newCLITUIBridge()
	for index := range cliArtifactLimit + 2 {
		bridge.RetainAudioArtifact(uint64(index+1), 1, api.AudioAnalysisStats, "Synthetic.\x1b]0;title\a.txt")
	}
	bridge.RetainAudioArtifact(1, 1, api.AudioAnalysisStats, strings.Repeat("x", cliQueueResultBytes+1))
	if len(bridge.artifacts) != cliArtifactLimit || bridge.artifactsDropped != 3 {
		t.Fatal("artifact history or oversized-path retention is unbounded")
	}
	for _, result := range bridge.artifacts {
		if len(result) > cliQueueResultBytes || strings.ContainsAny(result, "\x1b\a") || !strings.Contains(result, `\x1b`) {
			t.Fatal("durable local result did not preserve a safe escaped path")
		}
	}
	if !strings.Contains(bridge.summary(nil), "3 artifact paths omitted") {
		t.Fatal("artifact omission was silent")
	}
}

type cliAsyncAudioCore struct {
	cliWorkflowCoreFake
	module *releaseworkflow.Module
}

func (c *cliAsyncAudioCore) StartReleaseWorkflow(ctx context.Context, owner string, command releaseworkflow.Command) (api.WorkflowOperationStatus, error) {
	result, err := c.module.Start(ctx, owner, command)
	if err != nil {
		return result, fmt.Errorf("start test audio module: %w", err)
	}
	return result, nil
}

func (c *cliAsyncAudioCore) CurrentReleaseWorkflow(ctx context.Context, owner string, workflow api.WorkflowID) (releaseworkflow.CommandResult, error) {
	result, err := c.module.Current(ctx, owner, workflow)
	if err != nil {
		return result, fmt.Errorf("load test audio module: %w", err)
	}
	return result, nil
}

func (c *cliAsyncAudioCore) ReleaseWorkflowOperation(ctx context.Context, owner string, workflow api.WorkflowID, operation api.WorkflowOperationID) (api.WorkflowOperationStatus, error) {
	result, err := c.module.Operation(ctx, owner, workflow, operation)
	if err != nil {
		return result, fmt.Errorf("poll test audio module: %w", err)
	}
	return result, nil
}

func (c *cliAsyncAudioCore) ReleaseWorkflowOperationEvents(ctx context.Context, owner string, workflow api.WorkflowID, operation api.WorkflowOperationID, after uint64, limit int) ([]api.WorkflowEvent, error) {
	result, err := c.module.OperationEvents(ctx, owner, workflow, operation, after, limit)
	if err != nil {
		return result, fmt.Errorf("poll test audio events: %w", err)
	}
	return result, nil
}

type cliProgressAudioBuilder struct{ proceed <-chan struct{} }

func (b cliProgressAudioBuilder) Build(ctx context.Context, _ api.ReleaseRef, instructions api.AudioAnalysisInstructions,
	_ string, _ time.Time, _ *api.AudioAnalysisResult, _ releaseworkflow.RetainedAudioAnalysisResource,
) (api.AudioAnalysisResult, releaseworkflow.RetainedAudioAnalysisResource, error) {
	// The decoder's actual contract uses ItemOnly percentages; Module.Start
	// replaces the CLI callback and persists these updates before polling.
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "audio_analysis_decode",
		ItemID:    "track-1",
		Kind:      "audio_track",
		Label:     "Audio track 1",
		Status:    api.StageStatusRunning,
		Completed: 75,
		Total:     100,
		ItemOnly:  true,
	})
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("test audio canceled: %w", ctx.Err())
	}
	return api.AudioAnalysisResult{
		ResourceID:          instructions.ResourceID,
		ManifestFingerprint: "manifest-1",
		Selection:           instructions.Selection,
		TrackIDs:            slices.Clone(instructions.TrackIDs),
		Variants:            slices.Clone(instructions.Variants),
		ProfileVersion:      instructions.ProfileVersion,
		ResourceLimits:      instructions.ResourceLimits,
		Status:              api.StageStatusFailed,
		Tracks: []api.AudioAnalysisTrackResult{{
			TrackID: "track-1",
			Ordinal: 1,
			Status:  api.StageStatusFailed,
			Failure: &api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "Synthetic failed analysis"},
		}},
	}, nil, nil
}

func TestAttachedAudioProgressCrossesActualAsyncModuleBoundary(t *testing.T) {
	proceed := make(chan struct{})
	preparedRelease := cliAudioAnalysisCurrentForTest().Release.Release
	module, err := releaseworkflow.New(releaseworkflow.NewMemoryRepository(), releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{
			PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				preparedRelease.Source.SourcePath = input.SourcePath
				return api.PrepareResult{Release: preparedRelease}, nil
			},
			DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				return api.PreparedReleaseDisplay{ReleaseName: "Synthetic.Film.2026-GRP"}, nil
			},
		}, releaseworkflow.WithAudioAnalysisBuilder(cliProgressAudioBuilder{proceed: proceed}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := module.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	created, err := module.Execute(t.Context(), cliWorkflowOwnerID, releaseworkflow.CreateWorkflowCommand{WorkflowID: "audio-progress"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := module.Execute(t.Context(), cliWorkflowOwnerID, releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: preparedRelease.Source.SourcePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge := newCLITUIBridge()
	writer := &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: bridge,
		ctx:       t.Context(),
		terminal:  true,
	}
	streams := cliIO{
		out:       writer,
		presenter: bridge,
		errOut:    io.Discard,
	}
	ctx := withCLIItemPresentation(t.Context(), streams, preparedRelease.Source.SourcePath, 1)
	session := &cliWorkflowSession{
		core:          &cliAsyncAudioCore{module: module},
		current:       current,
		streams:       streams,
		uploadRequest: api.Request{Options: api.UploadOptions{AudioTracks: "primary", AudioImages: "waveform"}},
	}
	done := make(chan error, 1)
	go func() { done <- session.completeAudioAnalysis(ctx) }()
	defer func() {
		close(proceed)
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "status failed") {
				t.Errorf("terminal failed analysis result = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("CLI poller did not join after releasing the audio builder")
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		bridge.mu.Lock()
		rows := slices.Clone(bridge.telemetry)
		view := bridge.latest
		aggregate := bridge.progress
		bridge.mu.Unlock()
		if len(rows) > 0 {
			row := rows[0]
			if !row.ItemOnly || row.Completed != 75 || row.Total != 100 || row.Lane != "track-1" ||
				row.WorkflowID != string(current.Workflow.ID) || row.OperationID != string(view.OperationID) ||
				row.Epoch != view.Epoch || aggregate != nil || view.Completed == 75 {
				t.Fatal("polling lost exact audio item authority or advanced aggregate progress")
			}
			// Preserve only same-operation telemetry at the model boundary.
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model.Update(cliBridgeUpdate{view: &view, telemetry: rows})
			if len(model.telemetry) != 1 {
				t.Fatal("current persisted track progress did not reach the model")
			}
			view.OperationID = "next-operation"
			model.Update(cliBridgeUpdate{view: &view, telemetry: rows})
			if len(model.telemetry) != 0 {
				t.Fatal("old-operation audio progress survived a new operation")
			}
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("analysis exited before its persisted item progress: %v", err)
		case <-deadline.C:
			t.Fatal("asynchronous module progress never reached the CLI poller")
		case <-ticker.C:
		}
	}
}

func TestSubmissionExclusionsRemainTruthfulForDefaultAndMixedSelections(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(strconv.FormatBool(mixed), func(t *testing.T) {
			bridge := newCLITUIBridge()
			writer := &cliPresentationWriter{
				Writer:    io.Discard,
				presenter: bridge,
				ctx:       t.Context(),
				terminal:  true,
			}
			current := releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{
				ID:                   "history",
				Revision:             1,
				Status:               api.WorkflowStatusCompleted,
				SubmissionExclusions: []api.SubmissionExclusion{{TrackerID: "ALPHA", Reason: "already_uploaded"}},
			}}
			if mixed {
				current.Workflow.Status = api.WorkflowStatusActive
				current.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
				current.Selection = &api.TrackerSelection{TrackerIDs: []api.TrackerID{"BETA"}}
				current.Projections = &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "BETA", Readiness: api.ReadinessStatusReady}}}
			}
			session := &cliWorkflowSession{current: current, streams: cliIO{out: writer, presenter: bridge}}
			session.publishPresentation(t.Context())
			publishCLIItemResult(session.streams, nil)
			if !strings.Contains(bridge.summary(nil), "ALPHA: Already uploaded (already_uploaded)") {
				t.Fatal("confirmed exclusion disappeared with cleared downstream selection")
			}
			if !mixed && !strings.Contains(bridge.summary(nil), "Already uploaded to all selected trackers") {
				t.Fatal("all-confirmed terminal result became generic completion")
			}
			session.publishDeclinedAction(api.RequiredAction{Kind: api.RequiredActionApproveTrackers, Options: []api.RequiredActionOption{{Value: "BETA"}}})
			summary := bridge.summary(nil)
			if !strings.Contains(summary, "ALPHA: Already uploaded (already_uploaded)") || (mixed && !strings.Contains(summary, "BETA: Declined (not_approved)")) {
				t.Fatal("declining new candidates overwrote confirmed submission history")
			}
		})
	}
}

func TestCompactReleaseDetailsBesideArgumentsWithoutHeaders(t *testing.T) {
	bridge := newCLITUIBridge()
	current := cliAudioAnalysisCurrentForTest()
	current.Release.Release.Naming.ReleaseName = "Synthetic.Film.2026-GRP"
	current.Release.Release.Media.VideoCodec = "HEVC"
	current.Release.Release.Media.BitDepth = "10"
	current.Release.Release.Media.Audio = "Synthetic audio"
	current.Release.Release.Media.AudioLanguages = []string{"English", "French"}
	current.Release.Release.Media.SubtitleLanguages = []string{"Spanish", "German"}
	current.Release.Release.Identity = api.ExternalIdentity{
		TMDBID:   101,
		IMDBID:   202,
		TVDBID:   303,
		TVmazeID: 404,
		MALID:    505,
	}
	current.Release.Display.Providers = []api.ProviderDisplay{{
		SummaryAvailable: true,
		Summary: api.ProviderDisplaySummary{
			Title:    "Synthetic provider title",
			Overview: "Synthetic provider overview",
		},
	}}
	current.Release.Diagnostics = []api.PreparationDiagnostic{{Severity: api.DiagnosticSeverityWarning, Message: "Synthetic warning"}}
	session := &cliWorkflowSession{
		current:       current,
		intent:        cliWorkflowIntent{sourcePath: current.Release.Release.Source.SourcePath},
		uploadRequest: api.Request{Trackers: []string{"ALPHA"}},
		streams: cliIO{
			out:       &cliPresentationWriter{Writer: io.Discard, presenter: bridge},
			presenter: bridge,
		},
	}
	publishCLIInvocation(session.streams, []string{"--trackers=ALPHA"}, false)
	ctx := withCLIItemPresentation(t.Context(), session.streams, session.intent.sourcePath, 2)
	session.publishPresentation(ctx)
	view := bridge.latest
	for _, excluded := range []string{"Release details", "Source:", "External IDs", "Database info", "Synthetic provider", "Warnings:"} {
		if strings.Contains(view.Summary, excluded) {
			t.Fatalf("compact release body contains %q: %q", excluded, view.Summary)
		}
	}
	for _, text := range []string{"Upload name: Synthetic.Film.2026-GRP", "TMDB: 101", "IMDb: tt0000202", "TVDB: 303", "TVmaze: 404", "MAL: 505", "Video: HEVC", "Audio: Synthetic audio", "Audio languages: English, French", "Subtitle languages: Spanish, German"} {
		if !strings.Contains(view.Summary, text) {
			t.Fatalf("compact release body omits %q: %q", text, view.Summary)
		}
	}
	for _, size := range [][2]int{{232, 60}, {160, 50}, {100, 30}, {80, 24}, {60, 20}} {
		model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
		model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model.Update(cliBridgeUpdate{view: &view, telemetry: []cliTelemetry{{
			Lane:      "prepare",
			Phase:     "screenshots",
			Completed: 1,
			Total:     2,
		}}})
		if model.panels[cliPanelArguments].GetContent() != "Item 1/2: "+view.Source+"\n"+view.Arguments {
			t.Fatalf("current input item at %v is not above CLI arguments", size)
		}
		model.focus = cliPanelRelease
		frame := ansi.Strip(model.View().Content)
		if strings.Contains(frame, "Input [") || strings.Contains(frame, "Release details") || strings.Contains(frame, "Verbose details") || strings.Contains(frame, "Source:") {
			t.Fatalf("release headings/source at %v: %q", size, frame)
		}
		if model.panels[cliPanelRelease].GetContent() != strings.TrimSpace(view.Summary) || strings.Contains(frame, "prepare: screenshots 1/2") {
			t.Fatalf("compact release details at %v contain unrequested detail", size)
		}
		if !strings.Contains(frame, "╔") {
			t.Fatalf("headerless release focus at %v is invisible without color", size)
		}
		if size[0] >= 80 {
			if !slices.ContainsFunc(strings.Split(frame, "\n"), func(line string) bool {
				return strings.Contains(line, "CLI arguments") && strings.Contains(line, "Upload name:")
			}) {
				t.Fatalf("release details at %v are not beside arguments", size)
			}
		}
		if size[0] >= 120 {
			if model.panels[cliPanelRelease].Height()+2 != 11 || model.panels[cliPanelArguments].Height()+3 != 11 {
				t.Fatalf("shared row at %v does not retain its combined height", size)
			}
			for _, text := range []string{"TMDB: 101", "IMDb: tt0000202", "Video: HEVC", "Audio: Synthetic audio", "Audio languages: English, French", "Subtitle languages: Spanish, German"} {
				if !strings.Contains(frame, text) {
					t.Fatalf("shared row at %v omits %q", size, text)
				}
			}
			if model.panels[cliPanelTrackers].Height()+3 != size[1]-11-max(6, size[1]/5)-1 {
				t.Fatalf("tracker panel at %v does not use the removed input panel's space", size)
			}
		}
	}
	for _, dashboard := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {100, 30}} {
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
			model.dashboard = dashboard
			model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			model.Update(cliBridgeUpdate{view: &view})
			frame := ansi.Strip(model.View().Content)
			if !strings.Contains(frame, "Application flow") || !strings.Contains(frame, "Trackers (1)") || !strings.Contains(frame, "ALPHA:") {
				t.Fatalf("compact dashboard=%t at %v hides progress or trackers: %q", dashboard, size, frame)
			}
			if model.panels[cliPanelTrackers].Height()+3 != size[1]-min(11, size[1]/3)-2 {
				t.Fatalf("compact dashboard=%t at %v leaves input panel space unused", dashboard, size)
			}
		}
	}
}

func TestTorrentHashProgressSeparatesPiecesFromWorkflowCounts(t *testing.T) {
	for _, tracker := range []string{"", "ALPHA"} {
		t.Run(tracker, func(t *testing.T) {
			bridge := newCLITUIBridge()
			streams := cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge}
			ctx := withCLIItemPresentation(t.Context(), streams, "Synthetic.Film.2026-GRP", 1)
			session := &cliWorkflowSession{
				streams: streams,
				current: releaseworkflow.CommandResult{
					Workflow: api.ReleaseWorkflow{ID: "hashing-progress", Revision: 3},
					Operation: &api.WorkflowOperationStatus{
						ID:         "hashing-operation",
						WorkflowID: "hashing-progress",
						Phase:      "review-uploads",
						Status:     api.StageStatusRunning,
						Completed:  800,
						Total:      1000,
					},
				},
			}
			ctx = session.presentationProgressContext(ctx)
			session.publishPresentation(ctx)
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model.Update(bridge.drain())
			for _, completed := range []int{2, 35, 70, 100} {
				api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
					Task:            "torrent",
					Tracker:         tracker,
					Status:          "running",
					CompletedPieces: completed,
					TotalPieces:     100,
				})
				model.Update(bridge.drain())
				frame := ansi.Strip(model.flow(90, 20))
				for _, text := range []string{"review-uploads / running", fmt.Sprintf("Torrent hashing: %d/100 pieces", completed), fmt.Sprintf("%d%%", completed), "Overall workflow progress", "80%"} {
					if !strings.Contains(frame, text) {
						t.Fatalf("hashing update omitted %q: %s", text, frame)
					}
				}
				if model.view.Completed != 800 || model.view.Total != 1000 || bridge.progress != nil {
					t.Fatal("torrent piece counts overwrote overall workflow progress")
				}
				api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
					Phase:     "review-uploads / running",
					Completed: 800,
					Total:     1000,
				})
				model.Update(bridge.drain())
				session.publishPresentation(ctx)
				model.Update(bridge.drain())
				if frame != ansi.Strip(model.flow(90, 20)) {
					t.Fatal("polling the workflow replaced the hashing bar")
				}
			}
			// Torrent ready/reuse/failure notifications carry no piece total.
			api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
				Task:    "torrent",
				Tracker: tracker,
				Status:  "completed",
			})
			model.Update(bridge.drain())
			if frame := ansi.Strip(model.flow(90, 20)); strings.Contains(frame, "Torrent hashing:") || !strings.Contains(frame, "80%") {
				t.Fatalf("torrent completion left a hashing bar or lost the workflow bar: %s", frame)
			}
			session.presentationProgressContext(ctx)
			api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
				Task:            "torrent",
				Tracker:         tracker,
				Status:          "running",
				CompletedPieces: 5,
				TotalPieces:     100,
			})
			model.Update(bridge.drain())
			if strings.Contains(ansi.Strip(model.flow(90, 20)), "Torrent hashing:") {
				t.Fatal("an earlier command restored stale hashing progress")
			}
		})
	}
}

func TestFocusReturnAnswersAndCompactTrackerLogsRemainScrollable(t *testing.T) {
	model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, false)
	model.Update(tea.WindowSizeMsg{Width: 232, Height: 60})
	for index := range 21 {
		model.view.Lanes = append(model.view.Lanes, cliLaneView{
			ID:     fmt.Sprintf("T%02d", index),
			State:  "Eligible",
			Detail: strings.Repeat("Full upload name and policy detail. ", 8),
		})
	}
	for index := range 80 {
		model.logs = append(model.logs, cliLogLine{Level: "INFO", Text: fmt.Sprintf("Synthetic log %d %s", index, strings.Repeat("long line ", 30))})
	}
	model.refreshPanels()
	reply := make(chan cliAnswer, 1)
	model.Update(cliPromptRequest{prompt: cliPrompt{
		ID:       1,
		Kind:     cliPromptConfirm,
		Question: "Synthetic decision?",
		Evidence: strings.Repeat("Long evidence ", 400),
	}, reply: reply})
	model.Update(modelKey(tea.KeyTab))
	model.Update(modelKey(tea.KeyEnter))
	select {
	case <-reply:
		t.Fatal("inspection focus submitted a question")
	default:
	}
	for range cliPanelCount - 1 {
		model.Update(modelKey(tea.KeyTab))
	}
	model.Update(modelKey(tea.KeyRight))
	model.Update(modelKey(tea.KeyEnter))
	if answer := <-reply; !answer.Confirmed {
		t.Fatal("returning to the question left its answer control disabled")
	}
	frame := model.View().Content
	if lipgloss.Width(frame) != 232 || lipgloss.Height(frame) > 60 || strings.Contains(model.panels[cliPanelTrackers].View(), "Full upload name") {
		t.Fatal("realistic tracker content escaped the compact panel or layout dimensions")
	}
	for index := range 21 {
		if !strings.Contains(ansi.Strip(frame), fmt.Sprintf("T%02d:", index)) {
			t.Fatal("roomy tracker panel did not show all 21 compact rows")
		}
	}
	model.Update(modelKey(tea.KeyF7))
	model.Update(modelKey(tea.KeyPgUp))
	offset := model.panels[cliPanelLogs].YOffset()
	model.Update(cliBridgeUpdate{logs: []cliLogLine{{Level: "INFO", Text: "Incoming while paused"}}})
	if model.follow || model.panels[cliPanelLogs].YOffset() != offset || model.unseen != 1 {
		t.Fatal("log scrolling did not preserve its paused position")
	}
	model.Update(modelKey(tea.KeyEnd))
	if !model.follow || !model.panels[cliPanelLogs].AtBottom() {
		t.Fatal("End did not restore live log following")
	}
	if !strings.Contains(frame, "\x1b[") || ansi.Strip(frame) == frame {
		t.Fatal("colored model omitted terminal styling")
	}
}

func TestTrackerRulesAndReadinessReachColoredNames(t *testing.T) {
	rule := api.TrackerPolicyDecision{
		Code:     "single_file_folder",
		Blocking: true,
		Message:  "Single-file folders are not allowed.",
	}
	generic := []api.WorkflowFailure{{Failure: api.OperationFailure{Code: api.OperationFailureNoEligibleTrackers}}}
	action := api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, Status: api.RequiredActionStatusPending}
	resolved := api.RequiredAction{Kind: api.RequiredActionAuthorizeRules, Status: api.RequiredActionStatusResolved}
	for _, test := range []struct {
		name       string
		projection api.TrackerReleaseProjection
		outcome    api.TrackerLaneOutcome
		status     cliLaneStatus
		state      string
		reason     string
		color      string
	}{
		{
			name: "RULE",
			projection: api.TrackerReleaseProjection{
				Readiness:       api.ReadinessStatusIneligible,
				PolicyDecisions: []api.TrackerPolicyDecision{rule},
				Failures:        generic,
			},
			outcome: api.TrackerLaneOutcome{
				UploadEligibility: api.UploadEligibilitySkipped,
				UploadSkipReason:  api.UploadSkipReasonNotReady,
				UploadSkipDetail:  rule.Message,
				Failures:          generic,
			},
			status: cliLaneBlocked,
			reason: rule.Message,
			color:  "31",
		},
		{
			name: "RULE_CODE",
			projection: api.TrackerReleaseProjection{
				Readiness:       api.ReadinessStatusIneligible,
				PolicyDecisions: []api.TrackerPolicyDecision{{Code: "unsupported_edition", Blocking: true}},
				Failures:        generic,
			},
			outcome: api.TrackerLaneOutcome{
				UploadEligibility: api.UploadEligibilitySkipped,
				UploadSkipReason:  api.UploadSkipReasonNotReady,
				Failures:          generic,
			},
			status: cliLaneBlocked,
			reason: "unsupported_edition",
			color:  "31",
		},
		{
			name:       "AUTH",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusBlocked},
			outcome:    api.TrackerLaneOutcome{UploadEligibility: api.UploadEligibilitySkipped, Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{Code: "authentication_required", Message: "SECRET_SENTINEL"}}}},
			status:     cliLaneBlocked,
			reason:     "authentication_required",
			color:      "31",
		},
		{
			name:       "DUPE_CHECK",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusReady},
			outcome: api.TrackerLaneOutcome{
				Lifecycle:         api.OperationLifecycleReady,
				Disposition:       api.WorkflowDispositionNone,
				UploadEligibility: api.UploadEligibilityUnknown,
			},
			status: cliLanePending,
			color:  "33",
		},
		{
			name:       "QUESTIONNAIRE",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusBlocked, RequiredActions: []api.RequiredAction{resolved, action}},
			outcome:    api.TrackerLaneOutcome{UploadEligibility: api.UploadEligibilitySkipped, RequiredActions: []api.RequiredAction{resolved, action}},
			status:     cliLanePending,
			state:      "Awaiting decision",
			color:      "33",
		},
		{
			name:       "DUPE_CHECK_ACKNOWLEDGED",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusReady, RequiredActions: []api.RequiredAction{resolved}},
			outcome: api.TrackerLaneOutcome{
				Lifecycle:         api.OperationLifecycleReady,
				Disposition:       api.WorkflowDispositionNone,
				UploadEligibility: api.UploadEligibilityUnknown,
				RequiredActions:   []api.RequiredAction{resolved},
			},
			status: cliLanePending,
			state:  "Awaiting dupe check",
			color:  "33",
		},
		{
			name:       "READY",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusReady},
			outcome:    api.TrackerLaneOutcome{UploadEligibility: api.UploadEligibilityEligible},
			status:     cliLaneReady,
			color:      "32",
		},
		{
			name:       "READY_ACKNOWLEDGED",
			projection: api.TrackerReleaseProjection{Readiness: api.ReadinessStatusReady, RequiredActions: []api.RequiredAction{resolved}},
			outcome:    api.TrackerLaneOutcome{UploadEligibility: api.UploadEligibilityEligible, RequiredActions: []api.RequiredAction{resolved}},
			status:     cliLaneReady,
			state:      "Approved",
			color:      "32",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := newCLITUIBridge()
			test.projection.TrackerID, test.outcome.TrackerID = api.TrackerID(test.name), api.TrackerID(test.name)
			session := &cliWorkflowSession{
				streams:       cliIO{presenter: bridge},
				uploadRequest: api.Request{Trackers: []string{test.name}},
				current: releaseworkflow.CommandResult{
					Workflow:        api.ReleaseWorkflow{ID: "workflow", Revision: 1},
					Projections:     &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{test.projection}},
					Continuation:    api.WorkflowContinuation{TrackerOutcomes: []api.TrackerLaneOutcome{test.outcome}},
					TrackerApproval: &api.TrackerApprovalSnapshot{ApprovedTrackerIDs: []api.TrackerID{test.projection.TrackerID}},
				},
			}
			session.publishPresentation(t.Context())
			lane := bridge.latest.Lanes[0]
			if lane.Status != test.status || lane.Reason != test.reason || strings.Contains(cliLaneSummary(bridge.latest.Lanes), "SECRET_SENTINEL") {
				t.Fatalf("tracker lost retained status/reason or exposed remote detail: %#v", lane)
			}
			if test.state != "" && lane.State != test.state {
				t.Fatalf("resolved history changed the current readiness label: %q", lane.State)
			}
			if test.status == cliLanePending && lane.State == "Approved" {
				t.Fatal("prior approval hid an unresolved tracker prerequisite")
			}
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, false)
			model.Update(tea.WindowSizeMsg{Width: 232, Height: 60})
			model.Update(cliBridgeUpdate{view: &bridge.latest})
			panel := model.panels[cliPanelTrackers].View()
			if !strings.Contains(panel, "\x1b["+test.color+"m"+test.name) || !strings.Contains(ansi.Strip(panel), lane.State) {
				t.Fatalf("tracker name did not receive its readiness color: %q", panel)
			}
			model.noColor = true
			model.refreshPanels()
			panel = model.panels[cliPanelTrackers].View()
			if ansi.Strip(panel) != panel || !strings.Contains(panel, test.reason) {
				t.Fatal("NO_COLOR lost the reason or retained ANSI styling")
			}
		})
	}
}

var _ cliReleaseWorkflowCore = (*cliAsyncAudioCore)(nil)
var _ releaseworkflow.AudioAnalysisBuilder = cliProgressAudioBuilder{}

func TestRetainedSubmissionResultSurvivesGlobalDecline(t *testing.T) {
	bridge := newCLITUIBridge()
	writer := &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: bridge,
		ctx:       t.Context(),
		terminal:  true,
	}
	session := &cliWorkflowSession{
		streams:       cliIO{out: writer, presenter: bridge},
		uploadRequest: api.Request{Trackers: []string{"ALPHA", "BETA"}},
		current: releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: "workflow", Revision: 1},
			UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{
				TrackerID:             "ALPHA",
				SubmissionStatus:      api.StageStatusCompleted,
				ClientInjectionStatus: api.StageStatusFailed,
			}}}},
	}
	session.publishPresentation(t.Context())
	before := bridge.latest.Lanes[0]
	session.publishDeclinedAction(api.RequiredAction{Kind: api.RequiredActionReconcileSubmission})
	if bridge.latest.Lanes[0] != before || !strings.Contains(bridge.latest.Result, "External outcome remains unresolved") {
		t.Fatal("local reconciliation decline erased a recorded remote effect")
	}
}

func TestPausedLogEvictionPreservesVisibleRows(t *testing.T) {
	for _, prefix := range []string{"Short record", strings.Repeat("日本語é ", 100), "First line\nSecond line\nThird line"} {
		t.Run(strconv.Itoa(len(prefix)), func(t *testing.T) {
			model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, false)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model.logs = append(model.logs, cliLogLine{
				Time:  "12:00:00",
				Level: "INFO",
				Text:  prefix,
			})
			for index := 1; index < cliRetainedLogLimit; index++ {
				model.logs = append(model.logs, cliLogLine{
					Time:  "12:00:00",
					Level: "INFO",
					Text:  fmt.Sprintf("Retained record %d", index),
				})
			}
			model.refreshPanels()
			model.View() // Establish the log panel's actual dimensions.
			model.focus, model.follow = cliPanelLogs, false
			model.panels[cliPanelLogs].SetYOffset(30)
			before := model.panels[cliPanelLogs].View()
			model.Update(cliBridgeUpdate{logs: []cliLogLine{{Level: "INFO", Text: "Incoming record"}}})
			if after := model.panels[cliPanelLogs].View(); after != before || model.evicted != 1 || model.follow || model.unseen != 1 {
				t.Fatalf("evicting a record moved paused text: before=%q after=%q", before, after)
			}
			model.Update(modelKey(tea.KeyEnd))
			if !model.follow || !model.panels[cliPanelLogs].AtBottom() {
				t.Fatal("End did not resume following after eviction")
			}
		})
	}
}
