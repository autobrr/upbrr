// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLICompositeRetainsOutcomeExplanations(t *testing.T) {
	const preparationFailure = "Payload preparation failed; review the required input before retrying."
	const clientMessage = "Client delivery is unconfirmed; inspect the torrent client before retrying."
	const submissionFailure = "Tracker rejected the upload; review the required metadata."
	const receiptFailure = "Tracker accepted the upload, but its receipt could not be retained; inspect tracker history before continuing."
	const skipReason = "Duplicate evidence was found; this tracker was skipped."
	const blockedReason = "Required tracker input is missing; review the tracker questions."
	const reuseReason = "The matching torrent is already in the client; no new add was needed."
	for _, test := range []struct {
		name     string
		debug    bool
		current  releaseworkflow.CommandResult
		want     []string
		uploaded int
		failed   bool
	}{
		{
			name:  "debug preparation failure",
			debug: true,
			current: releaseworkflow.CommandResult{DryRun: &api.UploadDryRunResult{
				Status:         api.StageStatusBlocked,
				SucceededCount: 1,
				FailedCount:    1,
				SkippedCount:   1,
				Reports: []api.TrackerDryRunReport{{
					TrackerID:         "ALPHA",
					DisplayName:       "Alpha",
					Status:            api.StageStatusFailed,
					UploadReleaseName: "Example.Release.2026-GRP",
					Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{
						Code: api.OperationFailureMissingPrerequisite, Message: preparationFailure,
					}}},
					ClientInjection: api.ClientInjectionOutcome{Status: api.StageStatusSkipped, Message: "Client delivery skipped; no prepared payload."},
				}, {
					TrackerID:         "BETA",
					DisplayName:       "Beta",
					UploadReleaseName: "Example.Release.2026-GRP",
					Status:            api.StageStatusSkipped,
					Warnings:          []string{skipReason},
				}, {
					TrackerID:         "GAMMA",
					DisplayName:       "Gamma",
					UploadReleaseName: "Example.Release.2026-GRP",
					Status:            api.StageStatusBlocked,
					Failures:          []api.WorkflowFailure{{Failure: api.OperationFailure{Code: api.OperationFailureMissingPrerequisite, Message: blockedReason}}},
				}, {
					TrackerID:         "DELTA",
					DisplayName:       "Delta",
					UploadReleaseName: "Example.Release.2026-GRP",
					Status:            api.StageStatusCompleted,
					ClientInjection:   api.ClientInjectionOutcome{Status: api.StageStatusCompleted, Message: reuseReason},
				}},
			}},
			want: []string{preparationFailure, "Client delivery skipped; no prepared payload.", skipReason, blockedReason, reuseReason},
		},
		{
			name: "mixed submission and client outcomes",
			current: releaseworkflow.CommandResult{UploadResult: &api.UploadResult{
				Status: api.StageStatusPartial,
				Results: []api.UploadTrackerResult{
					{
						TrackerID:              "ALPHA",
						SubmissionStatus:       api.StageStatusCompleted,
						ClientInjectionStatus:  api.StageStatusFailed,
						ClientInjectionMessage: clientMessage + " passkey=SECRET_SENTINEL",
						ClientFailureCode:      api.OperationFailureUnknownOutcome,
						Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{
							Code:      api.OperationFailureUnknownOutcome,
							Operation: api.OperationKindClientInjection,
							Message:   clientMessage,
						}}},
					},
					{
						TrackerID:             "BETA",
						SubmissionStatus:      api.StageStatusFailed,
						ClientInjectionStatus: api.StageStatusPending,
						Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{
							Code: api.OperationFailureInvalidInput, Message: submissionFailure + "\x1b[2J",
						}}},
					},
					{
						TrackerID:             "GAMMA",
						SubmissionStatus:      api.StageStatusSkipped,
						ClientInjectionStatus: api.StageStatusSkipped,
					},
					{
						TrackerID:             "DELTA",
						SubmissionStatus:      api.StageStatusCompleted,
						ClientInjectionStatus: api.StageStatusUnavailable,
						Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{
							Code: api.OperationFailureReceiptRetention, Message: receiptFailure,
						}}},
					},
				},
			}},
			want:     []string{clientMessage, submissionFailure, receiptFailure},
			uploaded: 2,
			failed:   true,
		},
	} {
		for _, presentation := range []string{"plain", "tui"} {
			t.Run(test.name+"/"+presentation, func(t *testing.T) {
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
				core := &cliWorkflowCoreFake{current: test.current}
				ctx := withCLIItemPresentation(t.Context(), streams, "Example.Release.2026-GRP", 2)
				session := &cliWorkflowSession{
					core:          core,
					streams:       streams,
					current:       test.current,
					intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattended},
					uploadRequest: api.Request{SourcePath: "Example.Release.2026-GRP", Trackers: []string{"ALPHA", "BETA", "GAMMA", "DELTA"}},
				}
				uploaded, err := session.completeComposite(ctx, test.debug, bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{})
				if uploaded != test.uploaded || (err != nil) != test.failed || len(core.uploadRequests) != 1 || len(core.uploadFeedback) != 0 {
					t.Fatalf("presentation changed completion or requested feedback: uploaded=%d error=%v starts=%d feedback=%d", uploaded, err, len(core.uploadRequests), len(core.uploadFeedback))
				}
				text := output.String()
				panel := ""
				if presentation == "tui" {
					model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
					model.Update(tea.WindowSizeMsg{Width: 240, Height: 100})
					model.Update(cliBridgeUpdate{view: &bridge.latest})
					panel = model.panels[cliPanelTrackers].View()
					withCLIItemPresentation(t.Context(), streams, "Example.Next.Release.2026-GRP", 2)
					text = bridge.summary(err)
				}
				for _, want := range test.want {
					if !strings.Contains(text, want) {
						t.Errorf("retained outcome explanation missing %q", want)
					}
					if presentation == "tui" && !strings.Contains(panel, want) {
						t.Errorf("rendered tracker panel missing %q", want)
					}
				}
				if strings.Contains(text+panel, "SECRET_SENTINEL") || strings.Contains(text+panel, "\x1b[2J") {
					t.Fatal("outcome explanation bypassed diagnostic sanitization")
				}
				if presentation == "plain" && !test.debug && (!strings.Contains(text, "2 tracker upload(s), 1 failed, 1 skipped") || !strings.Contains(text, "Client injection incomplete: 2") || !strings.Contains(text, "do not resubmit accepted uploads")) {
					t.Fatal("mixed outcomes lost their submission counts or client-only recovery boundary")
				}
			})
		}
	}
}
