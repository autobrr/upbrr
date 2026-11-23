// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func (s *cliWorkflowSession) reconcileLegacyInputs(ctx context.Context) (result error) {
	var claim cliInputSlotClaim
	defer func() {
		result = errors.Join(result, s.releaseRecoveryInput(ctx, claim))
	}()
	active, err := s.core.GetActiveInputForSource(ctx, cliWorkflowOwnerID, s.intent.sourcePath)
	if err != nil {
		return fmt.Errorf("upbrr: inspect input recovery: %w", err)
	}
	for len(active.RecoveryWorkflowIDs) > 0 || (active.State == api.ActiveInputRecovering && active.Current != nil) {
		var workflowID api.WorkflowID
		if active.Current != nil {
			workflowID = active.Current.Workflow.ID
		} else if len(active.RecoveryWorkflowIDs) > 0 {
			workflowID = active.RecoveryWorkflowIDs[0]
		}
		active, err = s.core.RecoverLegacyActiveInput(ctx, cliWorkflowOwnerID, api.RecoverLegacyActiveInputRequest{WorkflowID: workflowID})
		if err != nil {
			return fmt.Errorf("upbrr: recover interrupted input: %w", err)
		}
		if active.Current != nil {
			if active.State != api.ActiveInputRecovering || active.Current.Workflow.ID != workflowID {
				return api.ErrActiveInputChanged
			}
			claim = cliInputSlotClaim{workflowID: workflowID, revision: active.Revision}
		}
		for active.Current != nil {
			action := pendingCLIWorkflowAction(active.Current.Workflow.RequiredActions, api.RequiredActionReconcileSubmission)
			if action == nil {
				return fmt.Errorf(
					"upbrr: interrupted input has no actionable reconciliation; inspect its workflow before retrying: %w",
					api.ErrReleaseWorkflowEffectOutcomeUnknown,
				)
			}
			priorRevision := active.Current.Workflow.Revision
			active, err = s.core.ReconcileActiveInput(ctx, cliWorkflowOwnerID, api.ReconcileActiveInputRequest{
				Authority: api.WorkflowAuthority{WorkflowID: workflowID, ExpectedRevision: priorRevision},
				Answer: api.RequiredActionAnswer{
					ActionID:         action.ID,
					WorkflowRevision: priorRevision,
					SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
				},
				IdempotencyKey: "auto-recover:" + string(action.ID),
			})
			if err != nil {
				return fmt.Errorf("upbrr: reconcile interrupted workflow %s: %w", workflowID, err)
			}
			s.logger.Infof("Recovery marked not completed: workflow=%s effect=%s", workflowID, action.EffectKind)
			if active.Current != nil && active.Current.Workflow.Revision <= priorRevision {
				return fmt.Errorf("upbrr: reconciliation did not advance workflow %s", workflowID)
			}
		}
		claim = cliInputSlotClaim{}
		active, err = s.core.GetActiveInputForSource(ctx, cliWorkflowOwnerID, s.intent.sourcePath)
		if err != nil {
			return fmt.Errorf("upbrr: inspect remaining input recovery: %w", err)
		}
	}
	s.inputBaseline = cliInputSlotBaseline{
		captured: true,
		state:    active.State,
		revision: active.Revision,
	}
	return nil
}

// releaseRecoveryInput detaches only the exact idle legacy claim made by this
// session. The backend also checks its coordinator; effects and actions remain.
func (s *cliWorkflowSession) releaseRecoveryInput(ctx context.Context, claim cliInputSlotClaim) error {
	if claim.workflowID == "" || claim.revision == 0 {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	active, err := s.core.GetActiveInput(cleanup, cliWorkflowOwnerID)
	if err != nil {
		return fmt.Errorf("upbrr: inspect recovery claim: %w", err)
	}
	if active.State != api.ActiveInputRecovering || active.Revision != claim.revision || active.Current == nil ||
		active.Current.Workflow.ID != claim.workflowID {
		return nil
	}
	if _, err := s.core.ReleaseActiveInput(cleanup, cliWorkflowOwnerID, api.ReleaseActiveInputRequest{ExpectedRevision: claim.revision}); err != nil {
		return fmt.Errorf("upbrr: release recovery claim: %w", err)
	}
	return nil
}
