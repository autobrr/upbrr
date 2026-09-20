// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"io"
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
					ID: "legacy",
 Revision: 3,
 RequiredActions: []api.RequiredAction{{
ID: "reconcile",
 WorkflowRevision: 3,
						Kind: api.RequiredActionReconcileSubmission,
 Status: api.RequiredActionStatusPending,
 Prompt: "Check the interrupted submission.",
}},
				}}}}
			session := &cliWorkflowSession{
core: core,
 intent: cliWorkflowIntent{interaction: test.mode},
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
