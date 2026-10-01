// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// RecoverLegacyInput claims the otherwise-empty migrated slot only to expose
// and reconcile effects created before active-input fencing existed. It never
// verifies source bytes, starts an operation, or resumes an external attempt.
func (m *Module) RecoverLegacyInput(ctx context.Context, ownerID string, workflowID api.WorkflowID) (api.ActiveInputRecord, error) {
	if err := m.requireActiveConfig(ctx); err != nil {
		return api.ActiveInputRecord{}, err
	}
	if m.activeInputs == nil || strings.TrimSpace(ownerID) == "" || workflowID == "" {
		return api.ActiveInputRecord{}, errors.New("release workflow: legacy recovery owner and workflow are required")
	}
	ownerID = strings.TrimSpace(ownerID)
	m.activeMu.Lock()
	defer m.activeMu.Unlock()

	if _, err := m.repository.Load(ctx, ownerID, workflowID); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow load legacy recovery workflow: %w", err)
	}
	slot, err := m.claimLegacyRecoverySlot(ctx, ownerID, workflowID)
	if err != nil {
		return api.ActiveInputRecord{}, err
	}
	recoveryCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{
		CoordinatorID: slot.CoordinatorID,
		Fence:         slot.Fence,
	})
	if err := m.interruptLegacyRecoveryOperations(recoveryCtx, ownerID, workflowID); err != nil {
		return api.ActiveInputRecord{}, err
	}
	effects, err := m.durability.RecoverLegacyEffects(recoveryCtx, ownerID, workflowID, m.clock.Now().UTC())
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow recover legacy effects: %w", err)
	}
	if err := m.publishLegacyRecoveryActions(recoveryCtx, ownerID, workflowID, effects); err != nil {
		return api.ActiveInputRecord{}, err
	}
	if err := m.finishLegacyInputRecoveryLocked(recoveryCtx, ownerID, workflowID); err != nil {
		return api.ActiveInputRecord{}, err
	}
	result, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow read recovered input: %w", err)
	}
	return result, nil
}

// LegacyRecoveryWorkflowIDs lists an owner's migrated workflows requiring
// explicit reconciliation. It is a read-only query and only applies when the
// shared input slot is empty.
func (m *Module) LegacyRecoveryWorkflowIDs(ctx context.Context, ownerID string) ([]api.WorkflowID, error) {
	if m.activeInputs == nil || strings.TrimSpace(ownerID) == "" {
		return nil, nil
	}
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return nil, fmt.Errorf("release workflow read recovery input: %w", err)
	}
	if slot.State != api.ActiveInputEmpty {
		return nil, nil
	}
	workflowIDs, err := m.durability.ListLegacyRecoveryWorkflowIDs(ctx, strings.TrimSpace(ownerID))
	if err != nil {
		return nil, fmt.Errorf("release workflow list recovery workflows: %w", err)
	}
	return workflowIDs, nil
}

func (m *Module) claimLegacyRecoverySlot(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ActiveInputRecord, error) {
	now := m.clock.Now().UTC()
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow read legacy recovery input: %w", err)
	}
	if slot.State == api.ActiveInputRecovering && IsLegacyRecoverySlot(slot) &&
		slot.OwnerID == ownerID && slot.WorkflowID == workflowID && slot.CoordinatorID == m.processEpoch && slot.LeaseExpiresAt.After(now) {
		return slot, nil
	}
	var next api.ActiveInputRecord
	switch {
	case slot.State == api.ActiveInputEmpty:
		next = api.ActiveInputRecord{
			State:          api.ActiveInputRecovering,
			Revision:       slot.Revision + 1,
			Fence:          slot.Fence + 1,
			OwnerID:        ownerID,
			CoordinatorID:  m.processEpoch,
			WorkflowID:     workflowID,
			LeaseExpiresAt: now.Add(workflowWorkLeaseTTL),
		}
	case slot.State == api.ActiveInputRecovering && IsLegacyRecoverySlot(slot) &&
		slot.OwnerID == ownerID && slot.WorkflowID == workflowID && !slot.LeaseExpiresAt.After(now):
		next = slot
		next.Revision, next.Fence = slot.Revision+1, slot.Fence+1
		next.CoordinatorID, next.LeaseExpiresAt = m.processEpoch, now.Add(workflowWorkLeaseTTL)
	default:
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, slot, next, now); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("release workflow claim legacy recovery input: %w", err)
	}
	m.startActiveInputHeartbeat(ctx, next.Fence)
	return next, nil
}

// IsLegacyRecoverySlot distinguishes a claimed legacy effect from an input
// being recovered after a previous process stopped.
func IsLegacyRecoverySlot(slot api.ActiveInputRecord) bool {
	return slot.State == api.ActiveInputRecovering && slot.InputID == "" && slot.SourceVersion == "" &&
		slot.WorkflowID != "" && slot.ReservationID == "" && slot.RequestedPath == ""
}

func (m *Module) interruptLegacyRecoveryOperations(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	operations, err := m.operations.ListActiveOperations(ctx)
	if err != nil {
		return fmt.Errorf("release workflow list legacy recovery operations: %w", err)
	}
	for _, operation := range operations {
		if operation.OwnerID != ownerID || operation.WorkflowID != workflowID {
			continue
		}
		if err := m.interruptRecoveredOperation(ctx, operation, "Operation interrupted for legacy effect reconciliation."); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) publishLegacyRecoveryActions(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	effects []api.ReleaseWorkflowEffectRecord,
) error {
	if len(effects) == 0 {
		return nil
	}
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow load legacy recovery actions: %w", err)
	}
	nextRevision := state.Workflow.Revision + 1
	now := m.clock.Now().UTC()
	changed := false
	for _, effect := range effects {
		if slices.ContainsFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionReconcileSubmission && action.Status == api.RequiredActionStatusPending &&
				action.EffectKind == api.WorkflowExternalEffectKind(effect.Kind) && action.EffectScopeID == effect.ScopeID
		}) {
			continue
		}
		trackerID := api.TrackerID("")
		if effect.Kind == string(api.WorkflowExternalEffectTrackerSubmission) {
			trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(effect.ScopeID)))
		} else if effect.Kind == string(api.WorkflowExternalEffectClientInjection) && strings.HasPrefix(effect.ScopeID, "upload:") {
			trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(effect.ScopeID, "upload:"))))
		}
		action, actionErr := m.newReconcileAction(
			nextRevision,
			now,
			trackerID,
			api.WorkflowExternalEffectKind(effect.Kind),
			effect.ScopeID,
			"Verify whether the interrupted external effect completed before allowing a fresh attempt.",
		)
		if actionErr != nil {
			return fmt.Errorf("release workflow create legacy recovery action: %w", actionErr)
		}
		state.Workflow.RequiredActions = append(state.Workflow.RequiredActions, action)
		changed = true
	}
	if !changed && state.ProcessEpoch == m.processEpoch {
		return nil
	}
	state.ProcessEpoch = m.processEpoch
	state.Workflow.Revision = nextRevision
	state.Workflow.UpdatedAt = now
	for index := range state.Workflow.RequiredActions {
		state.Workflow.RequiredActions[index].WorkflowRevision = nextRevision
	}
	if hasPendingRequiredAction(state.Workflow.RequiredActions) {
		state.Workflow.Status = api.WorkflowStatusBlocked
	}
	if err := state.Workflow.Validate(); err != nil {
		return fmt.Errorf("release workflow validate legacy recovery actions: %w", err)
	}
	if err := m.repository.Save(ctx, ownerID, nextRevision-1, state); err != nil {
		return fmt.Errorf("release workflow save legacy recovery actions: %w", err)
	}
	return nil
}

func (m *Module) legacyRecoveryMutationContext(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	command mutation,
) (context.Context, bool, error) {
	if m.activeInputs == nil {
		return ctx, false, nil
	}
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return ctx, true, fmt.Errorf("release workflow read legacy mutation input: %w", err)
	}
	if !IsLegacyRecoverySlot(slot) || slot.OwnerID != ownerID || slot.WorkflowID != workflowID {
		return ctx, false, nil
	}
	if _, ok := command.(ResolveActionCommand); !ok {
		return ctx, true, api.ErrActiveInputBusy
	}
	if slot.CoordinatorID != m.processEpoch || !slot.LeaseExpiresAt.After(m.clock.Now()) {
		return ctx, true, api.ErrActiveInputLeaseLost
	}
	if authority, ok := api.ActiveInputAuthorityFromContext(ctx); ok &&
		(authority.CoordinatorID != slot.CoordinatorID || authority.Fence != slot.Fence) {
		return ctx, true, api.ErrActiveInputLeaseLost
	}
	return api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: slot.CoordinatorID, Fence: slot.Fence}), true, nil
}

func (m *Module) finishLegacyInputRecovery(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	return m.finishLegacyInputRecoveryLocked(ctx, ownerID, workflowID)
}

// finishLegacyInputRecoveryLocked closes a legacy slot only after its effects
// and operations have been settled.
func (m *Module) finishLegacyInputRecoveryLocked(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	operations, err := m.operations.ListActiveOperations(ctx)
	if err != nil {
		return fmt.Errorf("release workflow inspect legacy recovery operations: %w", err)
	}
	if slices.ContainsFunc(operations, func(operation api.ReleaseWorkflowOperationRecord) bool {
		return operation.OwnerID == ownerID && operation.WorkflowID == workflowID
	}) {
		return nil
	}
	settled, err := m.settleRecoveryActions(ctx, ownerID, workflowID)
	if err != nil || !settled {
		return err
	}
	slot, err := m.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return fmt.Errorf("release workflow read legacy recovery completion input: %w", err)
	}
	if !IsLegacyRecoverySlot(slot) || slot.OwnerID != ownerID || slot.WorkflowID != workflowID {
		return api.ErrActiveInputChanged
	}
	empty := api.ActiveInputRecord{
		State:          api.ActiveInputEmpty,
		Revision:       slot.Revision + 1,
		Fence:          slot.Fence,
		OwnerID:        slot.OwnerID,
		CoordinatorID:  slot.CoordinatorID,
		LeaseExpiresAt: slot.LeaseExpiresAt,
	}
	if err := m.activeInputs.CompareAndSwapActiveInput(ctx, slot, empty, m.clock.Now().UTC()); err != nil {
		return fmt.Errorf("release workflow close legacy recovery input: %w", err)
	}
	if m.activeCancel != nil {
		m.activeCancel()
	}
	return nil
}

// settleRecoveryActions removes pending reconciliation actions only when no
// unresolved external effect remains for this workflow.
func (m *Module) settleRecoveryActions(ctx context.Context, ownerID string, workflowID api.WorkflowID) (bool, error) {
	workflowIDs, err := m.durability.ListLegacyRecoveryWorkflowIDs(ctx, ownerID)
	if err != nil {
		return false, fmt.Errorf("release workflow inspect recovery effects: %w", err)
	}
	if slices.Contains(workflowIDs, workflowID) {
		m.logger.Debugf("releaseworkflow: startup recovery decision=retain_actions reason=unresolved_effect workflow=%s", workflowID)
		return false, nil
	}
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return false, fmt.Errorf("release workflow load completed recovery: %w", err)
	}
	pending := slices.Clone(state.Workflow.RequiredActions)
	nextRevision := state.Workflow.Revision + 1
	now := m.clock.Now().UTC()
	settledCount := 0
	for _, action := range pending {
		if action.Kind != api.RequiredActionReconcileSubmission || action.Status != api.RequiredActionStatusPending {
			continue
		}
		settledCount++
		switch {
		case action.EffectKind == api.WorkflowExternalEffectImageHosting:
			finishUnavailableImageHostingReconciliation(&state.Workflow, action)
		case action.EffectKind == api.WorkflowExternalEffectClientInjection &&
			strings.HasPrefix(action.EffectScopeID, "upload:") && state.Workflow.UploadResult != nil:
			priorRef := *state.Workflow.UploadResult
			prior, ok := state.UploadResults[priorRef.ID]
			if !ok || prior.Revision != priorRef.Revision {
				invalidateUploadPlan(&state.Workflow)
				break
			}
			for _, result := range prior.Results {
				if !slices.ContainsFunc(result.Failures, func(failure api.WorkflowFailure) bool {
					return failure.Failure.Code == api.OperationFailureUnknownOutcome &&
						failure.Failure.Operation == api.OperationKindClientInjection && failure.Resource == action.EffectScopeID
				}) {
					continue
				}
				action.TrackerID = result.TrackerID
				if _, err := m.reconcileClientInjectionResult(ownerID, &state, nextRevision, now, action); err != nil {
					return false, fmt.Errorf("release workflow settle discarded client injection: %w", err)
				}
				break
			}
		default:
			if state.Workflow.DryRun != nil {
				m.private.Delete(ownerID, workflowID, uploadPlanPrivateResourceID(state.Workflow.DryRun.ID))
			}
			invalidateUploadPlan(&state.Workflow)
		}
		state.Workflow.Failures = slices.DeleteFunc(state.Workflow.Failures, func(failure api.WorkflowFailure) bool {
			if failure.Failure.Code != api.OperationFailureUnknownOutcome {
				return false
			}
			switch action.EffectKind {
			case api.WorkflowExternalEffectClientInjection, api.WorkflowExternalEffectImageHosting:
				return failure.Resource == action.EffectScopeID
			case api.WorkflowExternalEffectTrackerSubmission:
				return failure.TrackerID == action.TrackerID && failure.Failure.Operation == api.OperationKindUploadExecute
			default:
				return false
			}
		})
		state.Workflow.RequiredActions = slices.DeleteFunc(state.Workflow.RequiredActions, func(candidate api.RequiredAction) bool {
			return candidate.ID == action.ID
		})
	}
	if settledCount != 0 {
		if err := m.invalidateUnavailablePrivateAuthority(ownerID, &state.Workflow, now); err != nil {
			return false, fmt.Errorf("release workflow settle private authority: %w", err)
		}
		state.Workflow.Revision = nextRevision
		state.Workflow.UpdatedAt = now
		for index := range state.Workflow.RequiredActions {
			state.Workflow.RequiredActions[index].WorkflowRevision = state.Workflow.Revision
		}
		if state.Workflow.Status == api.WorkflowStatusBlocked && !hasPendingRequiredAction(state.Workflow.RequiredActions) {
			if state.Workflow.UploadResult != nil {
				state.Workflow.Status = api.WorkflowStatusCompleted
			} else {
				state.Workflow.Status = api.WorkflowStatusActive
			}
		}
		state.ProcessEpoch = m.processEpoch
		if err := state.Workflow.Validate(); err != nil {
			return false, fmt.Errorf("release workflow validate completed recovery: %w", err)
		}
		if err := m.repository.Save(ctx, ownerID, state.Workflow.Revision-1, state); err != nil {
			return false, fmt.Errorf("release workflow save completed recovery: %w", err)
		}
		m.logger.Debugf("releaseworkflow: startup recovery decision=discard_actions workflow=%s count=%d", workflowID, settledCount)
	}
	return true, nil
}
