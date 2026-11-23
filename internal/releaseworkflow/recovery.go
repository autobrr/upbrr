// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// Recovery exposes local reconciliation of retained external effects.
// It does not activate configuration or install preparation, tracker, image-host,
// or torrent-client services. Normal workflow admissions remain unavailable.
type Recovery struct {
	module *Module
}

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

// RecoverAutomatically applies the restart policy to one retained workflow.
// It uses the existing fenced disposition and never runs an external operation.
func (r *Recovery) RecoverAutomatically(ctx context.Context, scope api.WorkflowScope, logger api.Logger) (result error) {
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			slot, err := r.module.ActiveInput(cleanup, scope.OwnerID)
			if err == nil {
				err = r.module.releaseInterruptedOpenClaim(cleanup, scope, slot)
			}
			result = errors.Join(result, err)
		}
	}()
	retained, err := r.Recover(ctx, scope)
	if err != nil {
		return err
	}
	return r.resolveInterruptedActions(ctx, scope, retained.Workflow, logger)
}

func (r *Recovery) resolveInterruptedActions(ctx context.Context, scope api.WorkflowScope, workflow api.ReleaseWorkflow, logger api.Logger) error {
	for {
		index := slices.IndexFunc(workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending
		})
		if index < 0 {
			return nil
		}
		action := workflow.RequiredActions[index]
		next, err := r.markNotCompleted(ctx, scope, workflow.Revision, action.ID, "auto-recover:")
		if err != nil {
			return fmt.Errorf("mark interrupted %s effect not completed: %w", action.EffectKind, err)
		}
		workflow = next
		logger.Infof("Recovery marked not completed: workflow=%s effect=%s", scope.WorkflowID, action.EffectKind)
	}
}

// recoverSourceBeforeOpen settles retained attempts under the caller's exact
// slot revision. Only the caller's idle input can be detached for the requested open.
func (m *Module) recoverSourceBeforeOpen(ctx context.Context, owner, source, key string, revision uint64) (uint64, error) {
	prior, err := m.ActiveInput(ctx, owner)
	if err != nil || prior.Revision != revision {
		return revision, err
	}
	detach := prior.State == api.ActiveInputActive && m.OwnsActiveInput(prior) &&
		prior.IdempotencyKey != key
	if prior.State != api.ActiveInputEmpty && !detach {
		return revision, nil
	}
	ids, err := m.LegacyRecoveryWorkflowIDsForSource(ctx, owner, source)
	if err != nil || len(ids) == 0 {
		return revision, err
	}
	if detach {
		if err := m.ReleaseInput(ctx, owner, revision); err != nil {
			return revision, err
		}
		revision++
	}
	for _, id := range ids {
		scope := api.WorkflowScope{OwnerID: owner, WorkflowID: id}
		slot, err := m.recoverLegacyInput(ctx, owner, id, &revision)
		if err != nil {
			return revision, fmt.Errorf("recover interrupted workflow %s: %w", id, errors.Join(err, m.releaseInterruptedOpenClaim(ctx, scope, slot)))
		}
		if slot.State == api.ActiveInputRecovering && m.OwnsActiveInput(slot) && slot.WorkflowID == id && slot.Revision == revision+1 {
			state, loadErr := m.repository.Load(ctx, owner, id)
			if loadErr == nil {
				loadErr = (&Recovery{module: m}).resolveInterruptedActions(ctx, scope, state.Workflow, m.logger)
			}
			if loadErr != nil {
				return revision, fmt.Errorf("recover interrupted workflow %s: %w", id, errors.Join(loadErr, m.releaseInterruptedOpenClaim(ctx, scope, slot)))
			}
			slot, err = m.ActiveInput(ctx, owner)
			if err != nil {
				return revision, err
			}
		}
		if slot.State != api.ActiveInputEmpty || slot.Revision != revision+2 || slot.CoordinatorID != m.processEpoch {
			return revision, api.ErrActiveInputChanged
		}
		revision = slot.Revision
	}
	return revision, nil
}

func (m *Module) releaseInterruptedOpenClaim(ctx context.Context, scope api.WorkflowScope, claimed api.ActiveInputRecord) error {
	if !IsLegacyRecoverySlot(claimed) || !m.OwnsActiveInput(claimed) || claimed.WorkflowID != scope.WorkflowID {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	current, err := m.ActiveInput(cleanup, scope.OwnerID)
	if err != nil {
		return err
	}
	if current.Revision != claimed.Revision || !m.OwnsActiveInput(current) {
		return nil
	}
	return m.ReleaseInput(cleanup, scope.OwnerID, claimed.Revision)
}

// ConfirmNotCompleted records explicit verification that one pending external
// effect did not complete. The claimed input fence and exact workflow revision
// protect the decision; successful effects and configuration remain unchanged.
func (r *Recovery) ConfirmNotCompleted(
	ctx context.Context, scope api.WorkflowScope, revision api.WorkflowRevision, actionID api.RequiredActionID,
) (api.ReleaseWorkflow, error) {
	return r.markNotCompleted(ctx, scope, revision, actionID, "recover:")
}

func (r *Recovery) markNotCompleted(
	ctx context.Context, scope api.WorkflowScope, revision api.WorkflowRevision, actionID api.RequiredActionID, receiptPrefix string,
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
		IdempotencyKey:   receiptPrefix + string(actionID),
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
