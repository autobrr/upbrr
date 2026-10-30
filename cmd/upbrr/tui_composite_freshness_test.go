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
	"sync"
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

type cliCompositeProjectionFixture struct{}

func (cliCompositeProjectionFixture) Build(_ context.Context, _ api.ReleaseSnapshot, _ api.UploadSubject,
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

// terminalSnapshotRepository pauses a real questionnaire checkpoint until Current
// has read it, then lets the worker publish its terminal revision before polling
// reads the operation. The retained revisions are never changed by the fixture.
type terminalSnapshotRepository struct {
	*releaseworkflow.MemoryRepository
	questionnaireSaved chan struct{}
	stateCaptured      chan struct{}
	terminalSaved      chan struct{}
	checkpointOnce     sync.Once
	captureOnce        sync.Once
	terminalOnce       sync.Once
}

type terminalSnapshotReadKey struct{}

func (r *terminalSnapshotRepository) Save(ctx context.Context, owner string, expected api.WorkflowRevision, state releaseworkflow.State) error {
	if err := r.MemoryRepository.Save(ctx, owner, expected, state); err != nil {
		return fmt.Errorf("save questionnaire checkpoint: %w", err)
	}
	for _, action := range state.Workflow.RequiredActions {
		if action.Kind == api.RequiredActionAnswerQuestionnaire && action.Status == api.RequiredActionStatusPending {
			r.checkpointOnce.Do(func() {
				close(r.questionnaireSaved)
				select {
				case <-r.stateCaptured:
				case <-ctx.Done():
				}
			})
			break
		}
	}
	return nil
}

func (r *terminalSnapshotRepository) Load(ctx context.Context, owner string, id api.WorkflowID) (releaseworkflow.State, error) {
	state, err := r.MemoryRepository.Load(ctx, owner, id)
	if err != nil {
		return state, fmt.Errorf("load polling checkpoint: %w", err)
	}
	if ctx.Value(terminalSnapshotReadKey{}) == true {
		r.captureOnce.Do(func() {
			close(r.stateCaptured)
			select {
			case <-r.terminalSaved:
			case <-ctx.Done():
			}
		})
		if err := ctx.Err(); err != nil {
			return releaseworkflow.State{}, fmt.Errorf("wait for terminal checkpoint: %w", err)
		}
	}
	return state, nil
}

func (r *terminalSnapshotRepository) SaveOperation(ctx context.Context, sequence uint64, record api.ReleaseWorkflowOperationRecord) error {
	if err := r.MemoryRepository.SaveOperation(ctx, sequence, record); err != nil {
		return fmt.Errorf("save terminal operation: %w", err)
	}
	if isTerminalCLIWorkflowOperation(record.Status.Status) {
		r.terminalOnce.Do(func() { close(r.terminalSaved) })
	}
	return nil
}

func TestCompositePollingRefreshesActualQuestionnaireAndStageSnapshots(t *testing.T) {
	preflightProceed := make(chan struct{})
	repo := &terminalSnapshotRepository{
		MemoryRepository:   releaseworkflow.NewMemoryRepository(),
		questionnaireSaved: make(chan struct{}),
		stateCaptured:      make(chan struct{}),
		terminalSaved:      make(chan struct{}),
	}
	module, err := releaseworkflow.New(repo, releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{
			PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				return api.PrepareResult{Release: api.PreparedRelease{
					Generation: 1,
					Source:     api.SourceManifest{SourcePath: input.SourcePath},
					Naming: api.NamingFacts{
						ReleaseName: cliCompositeOldName,
						Type:        "ENCODE",
						Source:      "BluRay",
						Resolution:  "1080p",
					},
					Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
					Media:    api.MediaFacts{Type: "ENCODE", Source: "BluRay"},
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
		}, releaseworkflow.WithTrackerProjectionBuilder(cliCompositeProjectionFixture{}),
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
	ctxRead, cancelRead := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelRead()
	select {
	case <-repo.questionnaireSaved:
	case <-ctxRead.Done():
		t.Fatal("composite producer never reached its questionnaire")
	}
	blocked, err := module.Current(context.WithValue(ctxRead, terminalSnapshotReadKey{}, true), cliWorkflowOwnerID, started.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Operation == nil || !isTerminalCLIWorkflowOperation(blocked.Operation.Status) {
		t.Fatal("composite producer did not publish its terminal operation")
	}
	action := firstPendingCLICompositeAction(blocked.Continuation.RequiredActions)
	if action == nil || action.Kind != api.RequiredActionAnswerQuestionnaire || blocked.Projections == nil {
		t.Fatalf("composite producer did not retain questionnaire projections: status=%s actions=%v", blocked.Operation.Status, blocked.Continuation.RequiredActions)
	}
	if blocked.Workflow.Revision < blocked.Operation.ResultRevision || action.WorkflowRevision != blocked.Workflow.Revision {
		t.Fatalf("terminal snapshot has stale authority: workflow=%d action=%d operation=%d",
			blocked.Workflow.Revision, action.WorkflowRevision, blocked.Operation.ResultRevision)
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
	deadline := time.Now().Add(5 * time.Second)
	for {
		bridge.mu.Lock()
		view := bridge.latest
		bridge.mu.Unlock()
		if view.Revision > blocked.Workflow.Revision && len(view.Lanes) == 1 && !strings.Contains(view.Lanes[0].Detail, cliCompositeOldName) &&
			strings.Contains(view.Lanes[0].Detail, cliCompositeNewName) {
			if view.OperationID != resumed.Operation.ID || view.Lanes[0].Status == cliLaneReady {
				t.Fatal("fresh composite snapshot lost operation identity or displayed unresolved authority as ready")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("intermediate composite snapshot did not replace prior questionnaire authority")
		}
		time.Sleep(10 * time.Millisecond)
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
