// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const cliCompositeOldName = "Synthetic.Film.2026.Original-GRP"
const cliCompositeNewName = "Synthetic.Film.2026.Reviewed-GRP"

type cliPresentationRefreshFailureCore struct {
	*cliWorkflowCoreFake
	refreshCalls int
}

func (c *cliPresentationRefreshFailureCore) CurrentReleaseWorkflow(
	context.Context, string, api.WorkflowID,
) (releaseworkflow.CommandResult, error) {
	c.refreshCalls++
	if c.refreshCalls == 1 {
		return releaseworkflow.CommandResult{}, errors.New("synthetic presentation refresh failure")
	}
	return c.current, nil
}

func TestPresentationRefreshFailurePreservesPollingAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   api.StageStatus
		canceled bool
	}{
		{
			name:   "terminal status retains snapshot then recovers",
			status: api.StageStatusCompleted,
		},
		{
			name:   "running status continues polling",
			status: api.StageStatusRunning,
		},
		{
			name:     "cancellation still cancels operation",
			status:   api.StageStatusRunning,
			canceled: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			operation := api.WorkflowOperationStatus{
				WorkflowID: "refresh-workflow",
				ID:         "refresh-operation",
				Operation:  api.OperationKindPreparation,
				Status:     test.status,
			}
			retained := releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: operation.WorkflowID, Revision: 1}}
			current := retained
			current.Workflow.Revision = 2
			completed := operation
			completed.Status = api.StageStatusCompleted
			core := &cliPresentationRefreshFailureCore{cliWorkflowCoreFake: &cliWorkflowCoreFake{current: current, operation: completed}}
			bridge := newCLITUIBridge()
			session := &cliWorkflowSession{
				core:           core,
				current:        retained,
				progressWriter: io.Discard,
				logger:         api.NopLogger{},
				streams: cliIO{
					out: &cliPresentationWriter{
						Writer:    &cliTUILogWriter{bridge: bridge},
						presenter: bridge,
						ctx:       ctx,
						terminal:  true,
					},
					presenter: bridge,
				},
			}
			if test.canceled {
				cancel()
			}
			result, err := session.waitForOperation(ctx, operation)
			if test.canceled {
				if !errors.Is(err, context.Canceled) || core.cancelCalls != 1 {
					t.Fatalf("refresh failure bypassed cancellation: err=%v calls=%d", err, core.cancelCalls)
				}
				return
			}
			if err != nil || result.Status != api.StageStatusCompleted || core.cancelCalls != 0 {
				t.Fatalf("presentation refresh aborted the operation: result=%s err=%v", result.Status, err)
			}
			if test.status == api.StageStatusCompleted {
				if session.current.Workflow.Revision != retained.Workflow.Revision || session.current.Operation.Status != operation.Status {
					t.Fatal("failed refresh replaced the retained snapshot or omitted operation progress")
				}
				if _, err := session.waitForOperation(ctx, operation); err != nil {
					t.Fatal(err)
				}
			}
			if session.current.Workflow.Revision != current.Workflow.Revision || bridge.latest.Revision != current.Workflow.Revision {
				t.Fatal("a later successful refresh did not replace the retained presentation")
			}
		})
	}
}

type cliCompositeProjectionFixture struct{ proceed <-chan struct{} }

func (b cliCompositeProjectionFixture) Build(ctx context.Context, _ api.ReleaseSnapshot, _ api.UploadSubject,
	trackers []api.TrackerID, instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	_ map[api.TrackerID]api.WorkflowFingerprint, execution api.WorkflowExecutionMode,
) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
	name := cliCompositeOldName
	actions := []api.RequiredAction{{
		Kind:      api.RequiredActionAnswerQuestionnaire,
		TrackerID: "BETA",
		Prompt:    "Choose edition.",
	}}
	if answer := instructions["BETA"].Questionnaire["edition"]; answer != nil {
		select {
		case <-b.proceed:
		case <-ctx.Done():
			return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
				fmt.Errorf("projection fixture canceled: %w", ctx.Err())
		}
		name, actions = cliCompositeNewName, nil
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(name)
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("fingerprint fixture projection: %w", err)
	}
	catalog, err := (api.TrackerCatalogSnapshot{CatalogVersion: "fixture-v1", Trackers: []api.TrackerCatalogDescriptor{{
		TrackerID:         "BETA",
		DisplayName:       "Beta",
		ProjectorVersion:  "v1",
		PolicyFingerprint: fingerprint,
	}}}).WithFingerprint()
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("fingerprint fixture catalog: %w", err)
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            "BETA",
		UploadReleaseName:    name,
		CanonicalReleaseName: name,
		DuplicateCriteria:    api.TrackerDuplicateCriteria{Name: name},
		CatalogFingerprint:   fingerprint,
		InputFingerprint:     fingerprint,
		ConfigFingerprint:    fingerprint,
		ProjectorFingerprint: fingerprint,
		CriteriaFingerprint:  fingerprint,
		Readiness:            api.ReadinessStatusReady,
		DupeReady:            true,
		UploadReady:          len(actions) == 0,
		RequiredActions:      actions,
		Questionnaire: []api.TrackerQuestionnaireRequirement{{
			Key:      "edition",
			Label:    "Edition",
			Kind:     "text",
			Required: true,
		}},
	}
	return catalog, api.TrackerRuntimeSnapshot{RuntimeGeneration: "fixture-v1", Trackers: []api.TrackerRuntimeEntry{{
			TrackerID:         "BETA",
			Configured:        true,
			ConfigFingerprint: fingerprint,
		}}}, api.TrackerSelection{TrackerIDs: slices.Clone(trackers)}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  fingerprint,
			PolicyFingerprint: fingerprint,
			ExecutionMode:     execution,
			Projections:       []api.TrackerReleaseProjection{projection},
			RequiredActions:   actions,
			Status:            api.StageStatusReady,
		}, nil
}

type cliCompositePreflightFixture struct{ proceed <-chan struct{} }

func (b cliCompositePreflightFixture) Build(ctx context.Context, _ api.UploadSubject, _ api.TrackerCatalogSnapshot,
	_ api.TrackerRuntimeSnapshot, initial api.TrackerReleaseProjectionSet, now time.Time,
) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
	projection := initial.Projections[0]
	if projection.UploadReleaseName == cliCompositeNewName {
		select {
		case <-b.proceed:
		case <-ctx.Done():
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("preflight fixture canceled: %w", ctx.Err())
		}
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
	if err != nil {
		return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint fixture preflight: %w", err)
	}
	return api.TrackerPreflightAssessment{
		InputFingerprint: fingerprint,
		ExecutionMode:    initial.ExecutionMode,
		ExpiresAt:        now.Add(time.Hour),
		Results: []api.TrackerPreflightResult{{
			TrackerID:             projection.TrackerID,
			State:                 api.TrackerPreflightStateReady,
			AuthReady:             true,
			ClaimsReady:           true,
			BannedGroupsReady:     true,
			RemoteMetadataReady:   true,
			ConfigFingerprint:     projection.ConfigFingerprint,
			ProjectionFingerprint: fingerprint,
			AssessedAt:            now,
			FreshUntil:            now.Add(time.Hour),
			RequiredActions:       projection.RequiredActions,
		}},
	}, slices.Clone(initial.Projections), nil
}

func TestCompositePollingRefreshesActualQuestionnaireAndStageSnapshots(t *testing.T) {
	projectionProceed, preflightProceed := make(chan struct{}), make(chan struct{})
	module, err := releaseworkflow.New(releaseworkflow.NewMemoryRepository(), releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{
			PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				return api.PrepareResult{Release: api.PreparedRelease{
					Generation: 1,
					Source:     api.SourceManifest{SourcePath: input.SourcePath},
					Naming:     api.NamingFacts{ReleaseName: cliCompositeOldName},
				}}, nil
			},
			DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				return api.PreparedReleaseDisplay{ReleaseName: cliCompositeOldName}, nil
			},
			SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
				return api.UploadSubject{
					SourcePath:  input.Release.SourcePath,
					Trackers:    input.Trackers,
					ReleaseName: cliCompositeOldName,
					Source:      "bluray",
					Type:        "movie",
				}, nil
			},
		}, releaseworkflow.WithTrackerProjectionBuilder(cliCompositeProjectionFixture{proceed: projectionProceed}),
		releaseworkflow.WithTrackerPreflightBuilder(cliCompositePreflightFixture{proceed: preflightProceed}))
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
	started, err := module.StartUpload(t.Context(), cliWorkflowOwnerID, api.CreateReleaseWorkflowUploadRequest{
		Source:         api.ReleaseWorkflowUploadSource{Path: `C:\releases\Synthetic.Film.2026-GRP`},
		Unattended:     &api.ReleaseWorkflowUploadUnattended{Confirm: true},
		Execution:      api.ReleaseWorkflowUploadExecution{Mode: api.ReleaseWorkflowUploadModeUpload},
		Trackers:       api.ReleaseWorkflowUploadTrackers{Include: []api.TrackerID{"BETA"}},
		IdempotencyKey: "freshness-start",
	})
	if err != nil {
		t.Fatal(err)
	}
	var blocked releaseworkflow.CommandResult
	deadline := time.Now().Add(5 * time.Second)
	for {
		blocked, err = module.Current(t.Context(), cliWorkflowOwnerID, started.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		if blocked.Operation != nil && isTerminalCLIWorkflowOperation(blocked.Operation.Status) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("composite producer never reached its questionnaire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	action := firstPendingCLICompositeAction(blocked.Continuation.RequiredActions)
	if action == nil || action.Kind != api.RequiredActionAnswerQuestionnaire || blocked.Projections == nil {
		t.Fatalf("composite producer did not retain questionnaire projections: status=%s actions=%v", blocked.Operation.Status, blocked.Continuation.RequiredActions)
	}
	bridge := newCLITUIBridge()
	streams := cliIO{out: &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: bridge,
		ctx:       t.Context(),
		terminal:  true,
	}, presenter: bridge}
	ctx, cancel := context.WithCancel(withCLIItemPresentation(t.Context(), streams, `C:\releases\Synthetic.Film.2026-GRP`, 1))
	defer cancel()
	session := &cliWorkflowSession{
		core:           &cliAsyncAudioCore{module: module},
		current:        blocked,
		streams:        streams,
		progressWriter: io.Discard,
		uploadRequest:  api.Request{Trackers: []string{"BETA"}},
	}
	session.publishPresentation(ctx)
	answer := "Reviewed"
	resumed, err := module.SubmitUploadFeedback(ctx, cliWorkflowOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: action.WorkflowRevision},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire,
			Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{TrackerID: "BETA", Answers: map[string]*string{"edition": &answer}}},
		IdempotencyKey: "freshness-answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, waitErr := session.waitForCompositeUpload(ctx, resumed); done <- waitErr }()
	defer func() {
		cancel()
		select {
		case waitErr := <-done:
			if !errors.Is(waitErr, context.Canceled) {
				t.Errorf("canceled composite wait = %v", waitErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("composite presentation poller failed to join")
		}
	}()
	for _, name := range []string{"", cliCompositeNewName} {
		deadline = time.Now().Add(5 * time.Second)
		for {
			bridge.mu.Lock()
			view := bridge.latest
			bridge.mu.Unlock()
			if view.Revision > blocked.Workflow.Revision && len(view.Lanes) == 1 && !strings.Contains(view.Lanes[0].Detail, cliCompositeOldName) &&
				(name == "" || strings.Contains(view.Lanes[0].Detail, name)) {
				if view.OperationID != resumed.Operation.ID || view.Lanes[0].Status == cliLaneReady {
					t.Fatal("fresh composite snapshot lost operation identity or displayed unresolved authority as ready")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("intermediate composite snapshot did not replace prior questionnaire authority (stage %q)", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if name == "" {
			close(projectionProceed)
		}
	}
}

func TestCompositeExecutionModeDoesNotInferDebugFromRetainedReview(t *testing.T) {
	for _, mode := range []string{"upload", "debug", "site-check", "live-test"} {
		t.Run(mode, func(t *testing.T) {
			current := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{ID: "mode-workflow", Revision: 3},
				DryRun:   &api.UploadDryRunResult{Reports: []api.TrackerDryRunReport{{TrackerID: "BETA", Status: api.StageStatusCompleted}}},
			}
			if mode == "upload" {
				current.UploadResult = &api.UploadResult{Results: []api.UploadTrackerResult{{TrackerID: "BETA", SubmissionStatus: api.StageStatusCompleted}}}
			}
			core := &cliWorkflowCoreFake{current: current, liveTest: mode == "live-test"}
			bridge := newCLITUIBridge()
			streams := cliIO{
				out: &cliPresentationWriter{
					Writer:    io.Discard,
					presenter: bridge,
					ctx:       t.Context(),
					terminal:  true,
				},
				presenter: bridge,
				errOut:    io.Discard,
			}
			ctx := withCLIItemPresentation(t.Context(), streams, "Synthetic.Film.2026-GRP", 1)
			session := &cliWorkflowSession{
				core:    core,
				streams: streams,
				current: current,
				uploadRequest: api.Request{
					SourcePath: "Synthetic.Film.2026-GRP",
					Trackers:   []string{"BETA"},
					Execution:  api.ExecutionOptions{SiteCheck: mode == "site-check"},
				},
			}
			_, err := session.completeComposite(ctx, mode == "debug", nil, config.Config{}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			bridge.mu.Lock()
			view := bridge.latest
			bridge.mu.Unlock()
			suppressed := mode != "upload"
			if view.Debug != suppressed || strings.Contains(bridge.summary(nil), "submission suppressed") != suppressed {
				t.Fatal("retained payload review was mistaken for execution intent")
			}
			if mode == "upload" && (!strings.HasPrefix(view.Lanes[0].State, "Submission: completed / Client:") || view.Lanes[0].Reason != "") {
				t.Fatalf("normal submission retained debug labels: %#v", view.Lanes[0])
			}
		})
	}
}
