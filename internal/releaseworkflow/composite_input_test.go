// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadAdoptsExactPreparedInput(t *testing.T) {
	t.Parallel()
	module, repository, _ := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "adopt-prepared")
	request.Execution.PreparedRelease = api.ReleaseWorkflowPreparedReleaseRequire
	session, _, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	base := testPreparer()
	var preparations []api.PrepareInput
	module.preparer = ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			preparations = append(preparations, input)
			return base.Prepare(ctx, input)
		},
		PrepareResolvedFunc: func(ctx context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
			preparations = append(preparations, input.Input)
			return base.PrepareResolved(ctx, input)
		},
		DisplayFunc:   base.DisplayFunc,
		SubjectFunc:   base.SubjectFunc,
		DuplicateFunc: base.DuplicateFunc,
	}
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-adopt"})
	preparedInput := *session.Intent.Preparation
	preparedInput.ExternalFreshness = api.ExternalFreshnessRefresh
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            preparedInput,
	})
	state, err := repository.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		t.Fatalf("load prepared workflow: %v", err)
	}
	release := state.Releases[state.Workflow.Release.ID]
	release.PreparationFingerprint = testFingerprint(t, "independent-request-lineage")
	release.Fingerprint, err = release.ComputeFingerprint()
	if err != nil {
		t.Fatalf("fingerprint prepared release: %v", err)
	}
	state.Releases[release.ID] = release
	state.Workflow.Revision++
	if err := repository.Save(t.Context(), testOwnerID, prepared.Workflow.Revision, state); err != nil {
		t.Fatalf("save independent prepared lineage: %v", err)
	}
	request.Authority = &api.WorkflowAuthority{WorkflowID: state.Workflow.ID, ExpectedRevision: state.Workflow.Revision}
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	if current.Workflow.ID != prepared.Workflow.ID || current.Release == nil || current.Release.Release.Generation != prepared.Release.Release.Generation {
		t.Fatalf("prepared input was replaced: before=%#v after=%#v", prepared.Workflow.Release, current.Workflow.Release)
	}
	if len(preparations) != 2 || preparations[0].ExternalFreshness != api.ExternalFreshnessRefresh ||
		!preparations[1].RequirePrepared || preparations[1].ExternalFreshness != api.ExternalFreshnessReuse {
		t.Fatalf("prepared input adoption calls = %#v", preparations)
	}
	replayed, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil || replayed.Workflow.ID != current.Workflow.ID {
		t.Fatalf("exact adoption replay = %#v, %v", replayed.Workflow, err)
	}
}

func TestCompositeUploadRejectsForcedPreparedAdoption(t *testing.T) {
	t.Parallel()
	module, _, _ := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "forced-adoption")
	session, _, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-forced-adoption"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            *session.Intent.Preparation,
	})
	request.Authority = &api.WorkflowAuthority{WorkflowID: prepared.Workflow.ID, ExpectedRevision: prepared.Workflow.Revision}
	request.Preparation.Force = true
	if _, err := module.StartUpload(t.Context(), testOwnerID, request); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("forced prepared adoption error = %v", err)
	}
}

func TestCreateWorkflowRetainsCompositeSourcePathBeforePreparation(t *testing.T) {
	t.Parallel()

	module, repository, _ := newCompositeUploadTestModule(t)
	session, _, err := normalizeCompositeUploadRequest(compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "durable-source"))
	if err != nil {
		t.Fatal(err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{Composite: session})
	state, err := repository.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("load created composite workflow: %v", err)
	}
	if state.SourcePath != session.Intent.Preparation.SourcePath {
		t.Fatalf("composite workflow source path = %q, want %q", state.SourcePath, session.Intent.Preparation.SourcePath)
	}
}

func TestCompositeContinuationIntentKeepsFeedbackForce(t *testing.T) {
	t.Parallel()
	initial := CommandResult{Release: &api.ReleaseSnapshot{}}
	session := &compositeUploadSession{Intent: api.WorkflowIntent{Preparation: &api.PrepareInput{Force: true}}}
	if intent := compositeContinuationIntent(initial, session); intent.Preparation == nil || !intent.Preparation.Force {
		t.Fatalf("forced continuation preparation = %#v", intent.Preparation)
	}
	session.Intent.Preparation.Force = false
	if intent := compositeContinuationIntent(initial, session); intent.Preparation != nil {
		t.Fatalf("hydrated continuation preparation = %#v", intent.Preparation)
	}
}
