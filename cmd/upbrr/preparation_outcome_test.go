// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIPreparationFailureRetainsReasonsBeforeFinalReport(t *testing.T) {
	for _, delivery := range []api.StageStatus{api.StageStatusFailed, api.StageStatusRunning} {
		for _, presentation := range []string{"plain", "tui"} {
			t.Run(string(delivery)+"/"+presentation, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					failure := api.OperationFailure{
						Code:      api.OperationFailureInvalidInput,
						Operation: api.OperationKindPreparation,
						Message:   "Source inspection failed; correct the input and retry.",
						Recovery:  api.OperationRecoveryEditInput,
					}
					operation := api.WorkflowOperationStatus{
						ID:         "operation-preparation",
						WorkflowID: "workflow-preparation",
						Operation:  api.OperationKindPreparation,
						Status:     api.StageStatusFailed,
						Failures: []api.WorkflowFailure{
							{Failure: failure},
							{Failure: api.OperationFailure{Message: "Metadata unavailable. token=synthetic-secret\x1b[2J"}},
						},
					}
					current := releaseworkflow.CommandResult{
						Workflow: api.ReleaseWorkflow{
							ID:       operation.WorkflowID,
							Revision: 2,
							Status:   api.WorkflowStatusFailed,
						},
						Operation: &operation,
					}
					core := &cliWorkflowCoreFake{current: current, operation: operation}
					core.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
						initial := operation
						initial.Status = delivery
						current.Operation = &initial
						return current, nil
					}
					var output strings.Builder
					bridge := newCLITUIBridge()
					streams := cliIO{out: &output, errOut: io.Discard}
					if presentation == "tui" {
						streams.out = &cliPresentationWriter{
							Writer:    &output,
							presenter: bridge,
							ctx:       t.Context(),
							terminal:  true,
						}
						streams.presenter = bridge
					}
					ctx := withCLIItemPresentation(t.Context(), streams, "Example.Release.2026-GRP.mkv", 2)
					session := &cliWorkflowSession{
						core:           core,
						streams:        streams,
						progressWriter: streams.out,
						logger:         api.NopLogger{},
						intent:         cliWorkflowIntent{interaction: api.InteractionModeUnattended},
					}
					err := session.executeContinuation(ctx, api.ContinueReleaseWorkflowRequest{
						Authority: &api.WorkflowAuthority{WorkflowID: operation.WorkflowID, ExpectedRevision: 1}, Goal: api.WorkflowGoalPrepared,
					})
					if got, ok := api.AsOperationFailure(err); !ok || got != failure {
						t.Fatalf("failure authority changed: %#v", got)
					}
					if len(core.continuations) != 1 || len(core.commands) != 0 || len(core.uploadRequests) != 0 || len(core.uploadFeedback) != 0 {
						t.Fatal("failure presentation started work or requested feedback")
					}
					text := output.String()
					if presentation == "tui" {
						model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
						model.Update(tea.WindowSizeMsg{Width: 240, Height: 100})
						model.Update(cliBridgeUpdate{view: &bridge.latest})
						text = model.panels[cliPanelRelease].View()
						publishCLIItemResult(streams, err, "")
						withCLIItemPresentation(t.Context(), streams, "Example.Next.Release.2026-GRP.mkv", 2)
						if !strings.Contains(bridge.summary(nil), failure.Message) {
							t.Error("queue rollover lost the failure explanation")
						}
					}
					for _, want := range []string{"will not proceed to tracker upload", failure.Message, "Metadata unavailable."} {
						if !strings.Contains(text, want) {
							t.Errorf("preparation outcome missing %q", want)
						}
					}
					if strings.ContainsAny(text, "\x1b\r") || strings.Contains(text, "synthetic-secret") {
						t.Error("preparation outcome was not sanitized")
					}
				})
			})
		}
	}
}
