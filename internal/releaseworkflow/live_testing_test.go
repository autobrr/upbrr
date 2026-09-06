// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestWorkflowRejectsMutationEntrypoints(t *testing.T) {
	t.Parallel()
	policy, err := api.NewLiveTestPolicy("workflow-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	uploads := &uploadPlanBuilderFake{testing: t}
	module, repository := newTestModule(t, testPreparer(), WithLiveTestPolicy(policy), WithUploadPlanBuilder(uploads))
	for _, command := range []Command{
		ExecuteUploadsCommand{}, RetryFailedUploadsCommand{}, RetryClientInjectionsCommand{},
		CompositeUploadCommand{Goal: api.WorkflowGoalUploaded},
	} {
		if _, err := module.Execute(t.Context(), testOwnerID, command); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
			t.Errorf("Execute(%T) = %v", command, err)
		}
		if _, err := module.Start(t.Context(), testOwnerID, command); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
			t.Errorf("Start(%T) = %v", command, err)
		}
	}
	if _, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalUploaded}); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Errorf("Continue(uploaded) = %v", err)
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "live-public-denial")
	if _, err := module.StartUpload(t.Context(), testOwnerID, request); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Errorf("StartUpload = %v", err)
	}
	if uploads.builds != 0 || uploads.execution != nil || uploads.clientRetries != 0 || len(repository.operations) != 0 {
		t.Fatalf("denied request dispatched work: uploads=%#v operations=%d", uploads, len(repository.operations))
	}
	snapshot := policy.Snapshot()
	if snapshot.TrackerSubmission != (api.LiveTestEffectCounts{RequestsDenied: 8}) ||
		snapshot.ClientMutation != (api.LiveTestEffectCounts{RequestsDenied: 2}) {
		t.Fatalf("request receipt = %#v", snapshot)
	}
}

func TestLiveTestCompositeUploadRetainsNormalRulesAndNoSeedAfterFeedback(t *testing.T) {
	t.Parallel()
	module, repository, uploads := newCompositeUploadTestModule(t)
	policy, err := api.NewLiveTestPolicy("composite-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := WithLiveTestPolicy(policy)(module); err != nil {
		t.Fatal(err)
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "live-safe-composite")
	request.Source.Path = filepath.Join(t.TempDir(), "Example.Release.2026-GRP")
	started, err := module.StartLiveTestUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "live-approve")
	if current.DryRun == nil || current.UploadResult != nil || current.Operation == nil || current.Operation.Status != api.StageStatusCompleted {
		t.Fatalf("safe completion = %#v", current)
	}
	if uploads.execution == nil || uploads.execution.executions != 0 || uploads.clientRetries != 0 || len(uploads.options) == 0 {
		t.Fatalf("unexpected upload calls = %#v", uploads)
	}
	for _, options := range uploads.options {
		if !options.DryRun || !options.NoSeed {
			t.Fatalf("unsafe build options after feedback = %#v", options)
		}
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Composite == nil || state.Composite.Goal != api.WorkflowGoalDryRun || !state.Composite.Intent.NoSeed ||
		state.Composite.Intent.ExecutionMode != api.WorkflowExecutionModeNormal {
		t.Fatalf("retained live-test session = %#v", state.Composite)
	}
	ordinary, _, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.RequestFingerprint == state.Composite.RequestFingerprint {
		t.Fatal("live-test receipt reused ordinary request fingerprint")
	}
	replayed, err := module.StartLiveTestUpload(t.Context(), testOwnerID, request)
	if err != nil || replayed.Workflow.ID != current.Workflow.ID || replayed.DryRun == nil || uploads.execution.executions != 0 {
		t.Fatalf("replay = %#v, error = %v", replayed, err)
	}
	if got := policy.Snapshot(); got.TrackerSubmission != (api.LiveTestEffectCounts{}) || got.ClientMutation != (api.LiveTestEffectCounts{}) {
		t.Fatalf("safe path attempted mutations: %#v", got)
	}
}

func TestLiveTestCompositeUploadDurableReplayRejectsMutationRetries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policy, err := api.NewLiveTestPolicy("durable-run", filepath.Join(root, "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	fixture, _, _ := newCompositeUploadTestModule(t)
	openModule := func(epoch string, now time.Time) (*Module, *db.SQLiteRepository, *uploadPlanBuilderFake) {
		t.Helper()
		repo, err := db.Open(filepath.Join(root, "workflow.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if err := repo.MigrateContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		persistent, err := NewPersistentRepository(repo)
		if err != nil {
			t.Fatal(err)
		}
		uploads := &uploadPlanBuilderFake{testing: t}
		module, err := New(persistent, NewMemoryPrivateResourceStore(), testPreparer(),
			WithClock(fixedClock{now: now}), WithProcessEpoch(epoch), WithLiveTestPolicy(policy),
			WithTrackerProjectionBuilder(fixture.trackerProjector), WithTrackerPreflightBuilder(fixture.trackerPreflight),
			WithDupeAssessmentBuilder(fixture.dupeBuilder), WithMediaArtifactBuilder(fixture.mediaBuilder),
			WithDescriptionBuilder(fixture.descriptionBuilder), WithUploadPlanBuilder(uploads))
		if err != nil {
			t.Fatal(err)
		}
		return module, repo, uploads
	}
	first, firstDB, firstUploads := openModule("before-restart", fixture.clock.Now())
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "durable-live-upload")
	request.Source.Path = filepath.Join(root, "Example.Release.2026-GRP")
	started, err := first.StartLiveTestUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, first, started)
	action := pendingCompositeTrackerApproval(t, blocked)
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: blocked.Workflow.Revision},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind:            api.ReleaseWorkflowUploadFeedbackTrackerApproval,
			TrackerApproval: &api.ReleaseWorkflowUploadTrackerApproval{Confirmed: true, TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
		},
		IdempotencyKey: "durable-live-approval",
	}
	resumed, err := first.SubmitUploadFeedback(t.Context(), testOwnerID, blocked.Workflow.ID, feedback)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitCompositeUploadTestOperation(t, first, resumed)
	if completed.DryRun == nil || completed.UploadResult != nil || completed.Operation.Status != api.StageStatusCompleted {
		t.Fatalf("safe result before restart = %#v", completed)
	}
	if err := first.waitForOperationCleanup(t.Context(), completed.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	// Both the repository and module are fresh; no private execution authority
	// crosses the process boundary, and the persisted feedback is replayed verbatim.
	// Restart recovery must happen later than the original operations: identical
	// timestamps otherwise make LatestOperation choose by random operation ID.
	restarted, _, restartedUploads := openModule("after-restart", fixture.clock.Now().Add(time.Minute))
	replayed, err := restarted.SubmitUploadFeedback(t.Context(), testOwnerID, completed.Workflow.ID, feedback)
	if err != nil {
		t.Fatalf("replay persisted feedback: %v", err)
	}
	if replayed.Operation == nil {
		t.Fatal("restarted feedback lost its durable operation")
	}
	if replayed.Operation.ID != completed.Operation.ID || replayed.Operation.Status != api.StageStatusStale ||
		len(replayed.Operation.Failures) != 1 || replayed.Operation.Failures[0].Failure.Code != api.OperationFailureStaleResult ||
		replayed.Operation.Failures[0].Failure.Recovery != api.OperationRecoveryReprepare || replayed.DryRun != nil || replayed.UploadResult != nil {
		t.Fatalf("restarted operation=%s status=%s failures=%#v dryRun=%t upload=%t", replayed.Operation.ID,
			replayed.Operation.Status, replayed.Operation.Failures, replayed.DryRun != nil, replayed.UploadResult != nil)
	}
	replayedRequest, err := restarted.StartLiveTestUpload(t.Context(), testOwnerID, request)
	if err != nil || replayedRequest.Workflow.ID != completed.Workflow.ID || replayedRequest.Operation == nil ||
		replayedRequest.Operation.ID != replayed.Operation.ID || replayedRequest.UploadResult != nil {
		t.Fatalf("restarted request workflow=%s operation=%#v error=%v", replayedRequest.Workflow.ID, replayedRequest.Operation, err)
	}
	state, err := restarted.repository.Load(t.Context(), testOwnerID, completed.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Composite == nil || state.Composite.Goal != api.WorkflowGoalDryRun || !state.Composite.Intent.NoSeed ||
		state.Composite.Intent.ExecutionMode != api.WorkflowExecutionModeNormal || state.Composite.FeedbackSequence != 1 {
		t.Fatalf("restarted session changed effective execution: %#v", state.Composite)
	}
	staleFeedback := feedback
	staleFeedback.IdempotencyKey = "stale-live-approval"
	if _, err := restarted.SubmitUploadFeedback(t.Context(), testOwnerID, completed.Workflow.ID, staleFeedback); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale feedback = %v", err)
	}
	for _, command := range []Command{
		ExecuteUploadsCommand{WorkflowID: completed.Workflow.ID, ExpectedRevision: completed.Workflow.Revision},
		RetryFailedUploadsCommand{WorkflowID: completed.Workflow.ID, ExpectedRevision: completed.Workflow.Revision},
		RetryClientInjectionsCommand{WorkflowID: completed.Workflow.ID, ExpectedRevision: completed.Workflow.Revision},
	} {
		if _, err := restarted.Start(t.Context(), testOwnerID, command); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
			t.Fatalf("restarted %T = %v", command, err)
		}
	}
	if _, err := restarted.StartUpload(t.Context(), testOwnerID, request); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("restarted normal submission = %v", err)
	}
	if _, err := restarted.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: completed.Workflow.ID, ExpectedRevision: blocked.Workflow.Revision},
		Goal:           api.WorkflowGoalUploaded,
		IdempotencyKey: "stale-upload-request",
	}); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("restarted stale upload request = %v", err)
	}
	if firstUploads.execution == nil || firstUploads.execution.executions != 0 || firstUploads.clientRetries != 0 ||
		restartedUploads.builds != 0 || restartedUploads.execution != nil || restartedUploads.clientRetries != 0 {
		t.Fatalf("restart dispatched effects: first=%#v restarted=%#v", firstUploads, restartedUploads)
	}
	for _, options := range firstUploads.options {
		if !options.DryRun || !options.NoSeed {
			t.Fatalf("durable preparation used unsafe options: %#v", options)
		}
	}
	if got := policy.Snapshot(); got.TrackerSubmission != (api.LiveTestEffectCounts{RequestsDenied: 4}) ||
		got.ClientMutation != (api.LiveTestEffectCounts{RequestsDenied: 1}) {
		t.Fatalf("restarted effect receipt = %#v", got)
	}
}
