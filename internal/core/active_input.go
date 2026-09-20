// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// GetActiveInput projects only the caller's current input and workflow.
func (c *Core) GetActiveInput(ctx context.Context, owner string) (api.ActiveInputSnapshot, error) {
	slot, err := c.workflow.ActiveInput(ctx, owner)
	if err != nil {
		return api.ActiveInputSnapshot{}, classifyOperationError(api.OperationKindPreparation, err)
	}
	view := activeInputView(slot)
	if slot.State != api.ActiveInputEmpty && !c.workflow.OwnsActiveInput(slot) {
		// Previous-process work remains protected, but startup must not load
		// its prepared release or implicitly resume its workflow.
		return api.ActiveInputSnapshot{State: api.ActiveInputRecovering, Revision: slot.Revision}, nil
	}
	if slot.WorkflowID != "" {
		current, currentErr := c.workflow.Current(ctx, owner, slot.WorkflowID)
		if currentErr != nil {
			return api.ActiveInputSnapshot{}, fmt.Errorf("get active input workflow: %w", currentErr)
		}
		// A switch committed while the workflow was read cannot yield a mixed snapshot.
		latest, readErr := c.workflow.ActiveInput(ctx, owner)
		if readErr != nil {
			return api.ActiveInputSnapshot{}, fmt.Errorf("read latest active input: %w", readErr)
		}
		if latest.Revision != slot.Revision {
			return api.ActiveInputSnapshot{}, api.ErrActiveInputChanged
		}
		view.Current = &current
	}
	if slot.State == api.ActiveInputEmpty {
		workflowIDs, recoveryErr := c.workflow.LegacyRecoveryWorkflowIDs(ctx, owner)
		if recoveryErr != nil {
			return api.ActiveInputSnapshot{}, fmt.Errorf("list active input recovery workflows: %w", recoveryErr)
		}
		view.RecoveryWorkflowIDs = workflowIDs
	}
	return view, nil
}

// RecoverLegacyActiveInput claims one selected migrated workflow for manual
// external-effect reconciliation. It never starts source verification or upload work.
func (c *Core) RecoverLegacyActiveInput(
	ctx context.Context,
	owner string,
	request api.RecoverLegacyActiveInputRequest,
) (api.ActiveInputSnapshot, error) {
	if err := request.Validate(); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("validate legacy recovery request: %w", err)
	}
	workflowIDs, err := c.workflow.LegacyRecoveryWorkflowIDs(ctx, owner)
	if err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("list legacy recovery workflows: %w", err)
	}
	if !slices.Contains(workflowIDs, request.WorkflowID) {
		slot, slotErr := c.workflow.ActiveInput(ctx, owner)
		if slotErr != nil {
			return api.ActiveInputSnapshot{}, fmt.Errorf("read legacy recovery input: %w", slotErr)
		}
		if slot.State == api.ActiveInputRecovering && slot.OwnerID == owner && slot.WorkflowID == request.WorkflowID {
			if _, recoverErr := c.workflow.RecoverLegacyInput(ctx, owner, request.WorkflowID); recoverErr != nil {
				return api.ActiveInputSnapshot{}, fmt.Errorf("reenter legacy input recovery: %w", recoverErr)
			}
			return c.GetActiveInput(ctx, owner)
		}
		return api.ActiveInputSnapshot{}, api.ErrActiveInputChanged
	}
	if _, err := c.workflow.RecoverLegacyInput(ctx, owner, request.WorkflowID); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("recover legacy input: %w", err)
	}
	return c.GetActiveInput(ctx, owner)
}

// ReconcileActiveInput resolves one exact uncertain-outcome action in the
// active input or a claimed legacy recovery slot.
func (c *Core) ReconcileActiveInput(
	ctx context.Context,
	owner string,
	request api.ReconcileActiveInputRequest,
) (api.ActiveInputSnapshot, error) {
	if err := request.Validate(); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("validate input reconciliation request: %w", err)
	}
	if _, err := c.workflow.Execute(ctx, owner, releaseworkflow.ResolveActionCommand{
		WorkflowID:       request.Authority.WorkflowID,
		ExpectedRevision: request.Authority.ExpectedRevision,
		Answer:           request.Answer,
		IdempotencyKey:   request.IdempotencyKey,
	}); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("reconcile input: %w", err)
	}
	return c.GetActiveInput(ctx, owner)
}

// OpenActiveInput verifies the source and advances its exact workflow toward input readiness.
func (c *Core) OpenActiveInput(ctx context.Context, owner string, request api.OpenActiveInputRequest) (api.ActiveInputSnapshot, error) {
	request.Request = applyContinuationPreparationDefaults(request.Request, c.metadataDefaults)
	if err := request.Validate(); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("validate active input request: %w", err)
	}
	request.Request.Intent.Preparation.ExternalFreshness = api.ExternalFreshnessRefresh
	slot, err := c.workflow.OpenInput(ctx, owner, releaseworkflow.OpenInputRequest{
		ExpectedRevision: request.ExpectedRevision,
		Input:            *request.Request.Intent.Preparation,
		IdempotencyKey:   request.Request.IdempotencyKey,
	})
	if err != nil {
		return api.ActiveInputSnapshot{}, classifyOperationError(api.OperationKindPreparation, err)
	}
	current, err := c.workflow.Current(ctx, owner, slot.WorkflowID)
	if err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("read opened input workflow: %w", err)
	}
	request.Request.Authority = &api.WorkflowAuthority{WorkflowID: slot.WorkflowID, ExpectedRevision: current.Workflow.Revision}
	current, err = c.ContinueReleaseWorkflow(ctx, owner, request.Request)
	if err != nil {
		return api.ActiveInputSnapshot{}, err
	}
	view := activeInputView(slot)
	view.Current = &current
	return view, nil
}

// ReleaseActiveInput closes the caller-owned input at the expected slot revision
// and reads the resulting snapshot. Busy work or changed authority leaves the close rejected;
// retained history and reusable artifacts are not deleted.
func (c *Core) ReleaseActiveInput(ctx context.Context, owner string, request api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error) {
	if err := c.workflow.ReleaseInput(ctx, owner, request.ExpectedRevision); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("release active input: %w", err)
	}
	return c.GetActiveInput(ctx, owner)
}

// releaseHistoryInput closes an idle matching input before the existing
// transactional purge guards check for concurrent work or a replacement input.
func (c *Core) releaseHistoryInput(ctx context.Context, sourcePath string) error {
	if c.workflow == nil || c.history == nil || c.history.activeInputs == nil {
		return nil
	}
	slot, err := c.history.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return fmt.Errorf("core: read history input: %w", err)
	}
	if slot.State == api.ActiveInputEmpty {
		return nil
	}
	paths := []string{slot.RequestedPath}
	if slot.InputID != "" {
		record, err := c.history.activeInputs.LoadInputRecordByID(ctx, slot.InputID)
		if err != nil {
			return fmt.Errorf("core: resolve history input: %w", err)
		}
		paths = append(paths, record.CanonicalPath)
	}
	for _, path := range paths {
		if path == "" || sourcePath == "" {
			continue
		}
		if releasePathRelated(c.history.fs, sourcePath, path) || releasePathRelated(c.history.fs, path, sourcePath) {
			if err := c.workflow.ReleaseInput(ctx, slot.OwnerID, slot.Revision); err != nil {
				return fmt.Errorf("core: close input for history deletion: %w", err)
			}
			return nil
		}
	}
	return nil
}

func activeInputView(slot api.ActiveInputRecord) api.ActiveInputSnapshot {
	view := api.ActiveInputSnapshot{
		State:    slot.State,
		Revision: slot.Revision,
		InputID:  slot.InputID,
	}
	if slot.InputID != "" {
		// Scope the public revision to the opaque input ID instead of exposing a content digest.
		digest := sha256.Sum256([]byte(slot.InputID + "\x00" + slot.SourceVersion))
		view.SourceVersion = hex.EncodeToString(digest[:])
	}
	return view
}
