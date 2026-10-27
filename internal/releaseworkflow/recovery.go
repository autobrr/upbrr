// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// Recovery exposes only manual reconciliation of retained external effects.
// It does not activate configuration or install preparation, tracker, image-host,
// or torrent-client services. Normal workflow admissions remain unavailable.
type Recovery struct {
	module *Module
}

// ErrManualRecoveryRequired marks retained scopes without the state and uncertain
// effect needed for exact-action reconciliation. Their fences remain untouched.
var ErrManualRecoveryRequired = errors.New("release workflow: retained scope requires manual recovery")

// RecoveryWorkflow identifies the retained source and its manual recovery actions.
type RecoveryWorkflow struct {
	Workflow   api.ReleaseWorkflow
	SourcePath string
}

// NewRecovery constructs a local maintenance session for an existing repository.
// The caller must shut it down before closing the repository.
func NewRecovery(repository Repository, activeInputs api.ActiveInputRepository, privateStore PrivateResourceStore) (*Recovery, error) {
	module, err := New(repository, privateStore, ReleasePreparerFunc{}, WithActiveInputs(
		activeInputs,
		func(context.Context, api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{}, errors.New("release workflow: source preparation is unavailable during recovery")
		},
	))
	if err != nil {
		return nil, err
	}
	return &Recovery{module: module}, nil
}

// Recover fences expired prior-process work and exposes its reconciliation actions.
// Live input and work leases remain protected, and source files are not inspected.
func (r *Recovery) Recover(ctx context.Context, scope api.WorkflowScope) (RecoveryWorkflow, error) {
	if _, err := r.module.repository.Load(ctx, scope.OwnerID, scope.WorkflowID); err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			return RecoveryWorkflow{}, ErrManualRecoveryRequired
		}
		return RecoveryWorkflow{}, fmt.Errorf("load recovery workflow: %w", err)
	}
	workflowIDs, err := r.module.durability.ListLegacyRecoveryWorkflowIDs(ctx, scope.OwnerID)
	if err != nil {
		return RecoveryWorkflow{}, fmt.Errorf("inspect recovery effects: %w", err)
	}
	if !slices.Contains(workflowIDs, scope.WorkflowID) {
		return RecoveryWorkflow{}, ErrManualRecoveryRequired
	}
	if err := r.module.ResetIdleInputOnStartup(ctx); err != nil {
		return RecoveryWorkflow{}, fmt.Errorf("reset interrupted input for recovery: %w", err)
	}
	if _, err := r.module.RecoverLegacyInput(ctx, scope.OwnerID, scope.WorkflowID); err != nil {
		return RecoveryWorkflow{}, err
	}
	state, err := r.module.repository.Load(ctx, scope.OwnerID, scope.WorkflowID)
	if err != nil {
		return RecoveryWorkflow{}, fmt.Errorf("load recovery workflow: %w", err)
	}
	sourcePath := state.SourcePath
	if sourcePath == "" && state.Workflow.Release != nil {
		if release, ok := state.Releases[state.Workflow.Release.ID]; ok {
			sourcePath = release.Release.Source.SourcePath
		}
	}
	return RecoveryWorkflow{Workflow: state.Workflow, SourcePath: sourcePath}, nil
}

// ConfirmNotCompleted records explicit verification that one pending external
// effect did not complete. The claimed input fence and exact workflow revision
// protect the decision; successful effects and configuration remain unchanged.
func (r *Recovery) ConfirmNotCompleted(
	ctx context.Context, scope api.WorkflowScope, revision api.WorkflowRevision, actionID api.RequiredActionID,
) (api.ReleaseWorkflow, error) {
	slot, err := r.module.ActiveInput(ctx, scope.OwnerID)
	if err != nil {
		return api.ReleaseWorkflow{}, err
	}
	if !IsLegacyRecoverySlot(slot) || !r.module.OwnsActiveInput(slot) || slot.WorkflowID != scope.WorkflowID {
		return api.ReleaseWorkflow{}, api.ErrActiveInputChanged
	}
	state, err := r.module.repository.Load(ctx, scope.OwnerID, scope.WorkflowID)
	if err != nil {
		return api.ReleaseWorkflow{}, fmt.Errorf("load recovery confirmation: %w", err)
	}
	if !slices.ContainsFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.ID == actionID && action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending
	}) {
		return api.ReleaseWorkflow{}, fmt.Errorf("%w: pending reconciliation action is required", ErrInvalidTransition)
	}
	result, err := r.module.Execute(ctx, scope.OwnerID, ResolveActionCommand{
		WorkflowID:       scope.WorkflowID,
		ExpectedRevision: revision,
		IdempotencyKey:   "recover:" + string(actionID),
		Answer: api.RequiredActionAnswer{
			ActionID:         actionID,
			WorkflowRevision: revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	})
	if err != nil {
		return api.ReleaseWorkflow{}, err
	}
	return result.Workflow, nil
}

// Shutdown joins recovery heartbeats and relinquishes any claimed input lease.
func (r *Recovery) Shutdown(ctx context.Context) error {
	return r.module.Shutdown(ctx)
}
