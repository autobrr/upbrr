// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

func (s *cliWorkflowSession) reconcileLegacyInputs(ctx context.Context, reader *bufio.Reader) error {
	active, err := s.core.GetActiveInput(ctx, cliWorkflowOwnerID)
	if err != nil {
		return fmt.Errorf("upbrr: inspect input recovery: %w", err)
	}
	for len(active.RecoveryWorkflowIDs) > 0 || (active.State == api.ActiveInputRecovering && active.Current != nil) {
		if s.intent.interaction == api.InteractionModeUnattended {
			return errors.New(
				"upbrr: an interrupted external effect requires reconciliation; rerun interactively or with --unattended_confirm after checking its outcome",
			)
		}
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
		for active.Current != nil {
			action := pendingCLIWorkflowAction(active.Current.Workflow.RequiredActions, api.RequiredActionReconcileSubmission)
			if action == nil {
				return errors.New("upbrr: interrupted input has no actionable reconciliation; inspect its workflow before retrying")
			}
			confirmed, err := promptYesNo(reader, s.streams.out, action.Prompt+" Confirm it did not complete? [y/N]: ", false)
			if err != nil {
				return fmt.Errorf("upbrr: read recovery confirmation: %w", err)
			}
			if !confirmed {
				return errors.New("upbrr: external outcome remains unresolved; no new submission was started")
			}
			priorRevision := active.Current.Workflow.Revision
			active, err = s.core.ReconcileActiveInput(ctx, cliWorkflowOwnerID, api.ReconcileActiveInputRequest{
				Authority: api.WorkflowAuthority{WorkflowID: workflowID, ExpectedRevision: priorRevision},
				Answer: api.RequiredActionAnswer{
					ActionID:         action.ID,
					WorkflowRevision: priorRevision,
					SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
				},
				IdempotencyKey: s.nextIdempotencyKey("reconcile"),
			})
			if err != nil {
				return fmt.Errorf("upbrr: reconcile interrupted input: %w", err)
			}
			if active.Current != nil && active.Current.Workflow.Revision <= priorRevision {
				return fmt.Errorf("upbrr: reconciliation did not advance workflow %s", workflowID)
			}
		}
	}
	s.inputBaseline = cliInputSlotBaseline{
		captured: true,
		state:    active.State,
		revision: active.Revision,
	}
	return nil
}
