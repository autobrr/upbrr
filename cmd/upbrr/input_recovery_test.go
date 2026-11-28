// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIInputRecoveryAutomaticallyResolvesExactAction(t *testing.T) {
	t.Parallel()
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm} {
		t.Run(string(mode), func(t *testing.T) {
			core := &cliWorkflowCoreFake{
				activeInput: api.ActiveInputSnapshot{State: api.ActiveInputEmpty, RecoveryWorkflowIDs: []api.WorkflowID{"legacy"}},
				recoveryInput: api.ActiveInputSnapshot{
					State:    api.ActiveInputRecovering,
					Revision: 7,
					Current: &api.ReleaseWorkflowCurrent{Workflow: api.ReleaseWorkflow{
						ID:       "legacy",
						Revision: 3,
						RequiredActions: []api.RequiredAction{{
							ID:               "reconcile",
							WorkflowRevision: 3,
							Kind:             api.RequiredActionReconcileSubmission,
							Status:           api.RequiredActionStatusPending,
						}},
					}},
				},
			}
			session := &cliWorkflowSession{
				core:   core,
				logger: api.NopLogger{},
				intent: cliWorkflowIntent{interaction: mode},
			}
			if err := session.reconcileLegacyInputs(t.Context()); err != nil || len(core.reconcileRequests) != 1 {
				t.Fatalf("reconcile calls=%d err=%v", len(core.reconcileRequests), err)
			}
			req := core.reconcileRequests[0]
			if req.Authority.WorkflowID != "legacy" || req.Authority.ExpectedRevision != 3 || req.Answer.ActionID != "reconcile" ||
				req.IdempotencyKey != "auto-recover:reconcile" || req.Answer.SelectedValues[0] != api.RequiredActionReconcileNotCompleted {
				t.Fatalf("wrong automatic recovery: %+v", req)
			}
		})
	}
}

func TestCLIInputRecoverySelectsOnlyTheRequestedSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.B.2026.mkv")
	core := &cliWorkflowCoreFake{
		activeInput: api.ActiveInputSnapshot{State: api.ActiveInputEmpty, RecoveryWorkflowIDs: []api.WorkflowID{"workflow-A"}},
		sourceRecoveryInputs: map[string]api.ActiveInputSnapshot{
			source: {State: api.ActiveInputEmpty, Revision: 7},
		},
	}
	session := &cliWorkflowSession{core: core, intent: cliWorkflowIntent{sourcePath: source, interaction: api.InteractionModeUnattended}}
	if err := session.reconcileLegacyInputs(t.Context()); err != nil {
		t.Fatalf("unrelated A recovery blocked B: %v", err)
	}
	if core.recoverCalls != 0 || len(core.reconcileRequests) != 0 || !session.inputBaseline.captured || session.inputBaseline.revision != 7 {
		t.Fatalf("unrelated recovery changed authority: recover=%d reconcile=%d baseline=%+v", core.recoverCalls, len(core.reconcileRequests), session.inputBaseline)
	}
	if len(core.recoverySourceQueries) != 1 || core.recoverySourceQueries[0] != source {
		t.Fatalf("recovery query lost its source: %v", core.recoverySourceQueries)
	}
}

func TestCLIInputRecoveryErrorReleasesOnlyItsClaim(t *testing.T) {
	failure := errors.New("recovery write failed")
	core := &cliWorkflowCoreFake{
		reconcileError: failure,
		activeInput:    api.ActiveInputSnapshot{State: api.ActiveInputEmpty, RecoveryWorkflowIDs: []api.WorkflowID{"workflow-A"}},
		recoveryInput: api.ActiveInputSnapshot{
			State:    api.ActiveInputRecovering,
			Revision: 11,
			Current: &api.ReleaseWorkflowCurrent{Workflow: api.ReleaseWorkflow{
				ID:       "workflow-A",
				Revision: 3,
				RequiredActions: []api.RequiredAction{{
					ID:               "exact-A-action",
					WorkflowRevision: 3,
					Kind:             api.RequiredActionReconcileSubmission,
					Status:           api.RequiredActionStatusPending,
				}},
			}},
		},
	}
	session := &cliWorkflowSession{
		core:   core,
		intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
		logger: api.NopLogger{},
	}
	if err := session.reconcileLegacyInputs(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("expected recovery error: %v", err)
	}
	if len(core.releaseRequests) != 1 || core.releaseRequests[0].ExpectedRevision != 11 || len(core.reconcileRequests) != 1 || core.activeInput.State != api.ActiveInputEmpty {
		t.Fatalf("failure retained the claim or repeated reconciliation: releases=%v reconciles=%v state=%s", core.releaseRequests, core.reconcileRequests, core.activeInput.State)
	}
}

func TestCLIInputRecoveryLeavesPreviousProcessInputForVerifiedOpen(t *testing.T) {
	t.Parallel()
	core := &cliWorkflowCoreFake{activeInput: api.ActiveInputSnapshot{State: api.ActiveInputRecovering, Revision: 7}}
	session := &cliWorkflowSession{core: core, intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm}}
	if err := session.reconcileLegacyInputs(t.Context()); err != nil {
		t.Fatalf("inspect previous-process input: %v", err)
	}
	if core.recoverCalls != 0 || !session.inputBaseline.captured || session.inputBaseline.revision != 7 {
		t.Fatalf("previous-process input was claimed as legacy recovery: calls=%d baseline=%#v", core.recoverCalls, session.inputBaseline)
	}
}
