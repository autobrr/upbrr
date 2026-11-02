// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIInputRecoveryRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		mode      api.InteractionMode
		input     string
		wantCalls int
	}{
		{"strict never prompts", api.InteractionModeUnattended, "yes\n", 0},
		{"default no", api.InteractionModeUnattendedConfirm, "\n", 0},
		{"confirmed absent effect", api.InteractionModeUnattendedConfirm, "yes\n", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := &cliWorkflowCoreFake{activeInput: api.ActiveInputSnapshot{State: api.ActiveInputEmpty, RecoveryWorkflowIDs: []api.WorkflowID{"legacy"}},
				recoveryInput: api.ActiveInputSnapshot{State: api.ActiveInputRecovering, Current: &api.ReleaseWorkflowCurrent{Workflow: api.ReleaseWorkflow{
					ID:       "legacy",
					Revision: 3,
					RequiredActions: []api.RequiredAction{{
						ID:               "reconcile",
						WorkflowRevision: 3,
						Kind:             api.RequiredActionReconcileSubmission,
						Status:           api.RequiredActionStatusPending,
						Prompt:           "Check the interrupted submission.",
					}},
				}}}}
			session := &cliWorkflowSession{
				core:    core,
				intent:  cliWorkflowIntent{interaction: test.mode},
				streams: cliIO{out: io.Discard},
			}

			err := session.reconcileLegacyInputs(t.Context(), bufio.NewReader(strings.NewReader(test.input)))
			if (err == nil) != (test.wantCalls > 0) || len(core.reconcileRequests) != test.wantCalls {
				t.Fatalf("reconcile calls=%d err=%v", len(core.reconcileRequests), err)
			}
			if test.mode == api.InteractionModeUnattended && core.recoverCalls != 0 {
				t.Fatal("strict mode claimed recovery before required confirmation")
			}
			if test.wantCalls > 0 && core.reconcileRequests[0].Answer.SelectedValues[0] != api.RequiredActionReconcileNotCompleted {
				t.Fatal("wrong reconciliation answer")
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
	if err := session.reconcileLegacyInputs(t.Context(), bufio.NewReader(strings.NewReader(""))); err != nil {
		t.Fatalf("unrelated A recovery blocked B: %v", err)
	}
	if core.recoverCalls != 0 || len(core.reconcileRequests) != 0 || !session.inputBaseline.captured || session.inputBaseline.revision != 7 {
		t.Fatalf("unrelated recovery changed authority: recover=%d reconcile=%d baseline=%+v", core.recoverCalls, len(core.reconcileRequests), session.inputBaseline)
	}
	if len(core.recoverySourceQueries) != 1 || core.recoverySourceQueries[0] != source {
		t.Fatalf("recovery query lost its source: %v", core.recoverySourceQueries)
	}
}

func TestCLIInputRecoveryDeclineReleasesOnlyItsClaim(t *testing.T) {
	for _, input := range []string{"no\n", ""} {
		t.Run(input, func(t *testing.T) {
			core := &cliWorkflowCoreFake{
				activeInput: api.ActiveInputSnapshot{State: api.ActiveInputEmpty, RecoveryWorkflowIDs: []api.WorkflowID{"workflow-A"}},
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
				core:    core,
				intent:  cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				streams: cliIO{out: io.Discard},
			}
			if err := session.reconcileLegacyInputs(t.Context(), bufio.NewReader(strings.NewReader(input))); err == nil {
				t.Fatal("decline/EOF resolved the unknown effect")
			}
			if len(core.releaseRequests) != 1 || core.releaseRequests[0].ExpectedRevision != 11 || len(core.reconcileRequests) != 0 || core.activeInput.State != api.ActiveInputEmpty {
				t.Fatalf("decline retained the claim or reconciled the effect: releases=%v reconciles=%v state=%s", core.releaseRequests, core.reconcileRequests, core.activeInput.State)
			}
		})
	}
}

func TestCLIInputRecoveryLeavesPreviousProcessInputForVerifiedOpen(t *testing.T) {
	t.Parallel()
	core := &cliWorkflowCoreFake{activeInput: api.ActiveInputSnapshot{State: api.ActiveInputRecovering, Revision: 7}}
	session := &cliWorkflowSession{core: core, intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm}}
	if err := session.reconcileLegacyInputs(t.Context(), bufio.NewReader(strings.NewReader(""))); err != nil {
		t.Fatalf("inspect previous-process input: %v", err)
	}
	if core.recoverCalls != 0 || !session.inputBaseline.captured || session.inputBaseline.revision != 7 {
		t.Fatalf("previous-process input was claimed as legacy recovery: calls=%d baseline=%#v", core.recoverCalls, session.inputBaseline)
	}
}
