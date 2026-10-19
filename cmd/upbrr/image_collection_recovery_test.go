// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIImageCollectionRecoveryRequiresConfirmationAndStopsBeforeRetry(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		mode      api.InteractionMode
		input     string
		confirmed bool
	}{
		{
			name:  "strict unattended",
			mode:  api.InteractionModeUnattended,
			input: "yes\n",
		},
		{
			name:  "default decline",
			mode:  api.InteractionModeUnattendedConfirm,
			input: "\n",
		},
		{
			name:  "explicit decline",
			mode:  api.InteractionModeUnattendedConfirm,
			input: "no\n",
		},
		{
			name:      "confirmed absent collection",
			mode:      api.InteractionModeUnattendedConfirm,
			input:     "yes\n",
			confirmed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			action := api.RequiredAction{
				ID:               "collection-confirmation",
				Kind:             api.RequiredActionReconcileSubmission,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: 3,
				EffectKind:       api.WorkflowExternalEffectImageHosting,
				EffectScopeID:    api.ImageCollectionEffectScope("cli-attempt"),
				Prompt:           "Check slow.pics before clearing this uncertain collection attempt.",
				Options: []api.RequiredActionOption{{
					Value: api.RequiredActionReconcileNotCompleted,
					Label: "I verified no collection was created",
				}},
			}
			blocked := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{
					ID:              "collection-workflow",
					Revision:        3,
					Status:          api.WorkflowStatusBlocked,
					RequiredActions: []api.RequiredAction{action},
				},
				Continuation: api.WorkflowContinuation{RequiredActions: []api.RequiredAction{action}},
			}
			cleared := releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{
				ID:       blocked.Workflow.ID,
				Revision: 4,
				Status:   api.WorkflowStatusActive,
			}}
			core := &cliWorkflowCoreFake{
				startUploadFn: func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
					return blocked, nil
				},
				feedbackFn: func(api.ReleaseWorkflowUploadFeedback) (releaseworkflow.CommandResult, error) {
					return cleared, nil
				},
			}
			var output bytes.Buffer
			source := filepath.Join(t.TempDir(), "Synthetic.Film.2026-GRP.mkv")
			session := &cliWorkflowSession{
				core:           core,
				streams:        cliIO{out: &output},
				idempotencyRun: "collection-recovery",
				intent:         cliWorkflowIntent{sourcePath: source, interaction: test.mode},
				uploadRequest: api.Request{
					SourcePath: source,
					Trackers:   []string{"ALPHA"},
					Options:    api.UploadOptions{Screens: 0, InteractionMode: test.mode},
				},
			}
			_, err := session.completeComposite(t.Context(), false, bufio.NewReader(strings.NewReader(test.input)), config.Config{}, api.NopLogger{})
			if (err != nil) != (test.mode == api.InteractionModeUnattended) {
				t.Fatalf("collection recovery error = %v", err)
			}
			if len(core.uploadRequests) != 1 || len(core.commands) != 0 || len(core.continuations) != 0 {
				t.Fatal("collection confirmation dispatched a new upload or continuation")
			}
			wantFeedback := 0
			if test.confirmed {
				wantFeedback = 1
			}
			if len(core.uploadFeedback) != wantFeedback {
				t.Fatalf("collection feedback calls = %d, want %d", len(core.uploadFeedback), wantFeedback)
			}
			if test.mode == api.InteractionModeUnattended {
				if strings.Contains(output.String(), "[y/N]") {
					t.Fatal("strict unattended recovery prompted")
				}
				return
			}
			if !strings.Contains(output.String(), "I verified no collection was created? [y/N]") {
				t.Fatalf("collection-specific confirmation absent: %q", output.String())
			}
			if test.confirmed {
				feedback := core.uploadFeedback[0]
				if feedback.Action.ID != action.ID || feedback.Action.WorkflowRevision != action.WorkflowRevision ||
					feedback.Response.Reconciliation == nil || feedback.Response.Reconciliation.Selection != api.RequiredActionReconcileNotCompleted ||
					session.current.Workflow.Revision != cleared.Workflow.Revision || !strings.Contains(output.String(), "Run the upload command again") {
					t.Fatalf("collection confirmation did not stop for a separate retry: feedback=%#v output=%q", feedback, output.String())
				}
			}
		})
	}
}
