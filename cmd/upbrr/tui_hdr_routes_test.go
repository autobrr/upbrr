// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRRoutesAcceptPresentationFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--hdr-analysis-only", "--hdr-output-dir=output"},
		{"--hdr-resume-workflow=retained-workflow"},
	} {
		args = append(args, "--ui=tui", "--ui-keep-open")
		opts, visited, _, err := parseCLIOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateCLIHDR(opts, visited); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHDRRoutesKeepOrdinaryOutputWithoutTerminalInput(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("main_settings: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--hdr-analysis-only", "--hdr-output-dir=output"}, "requires exactly one supported HDR"},
		{[]string{"--hdr-resume-workflow=retained-workflow", "--config=" + configPath}, "yaml"},
	} {
		args := test.args
		args = append(args, "--ui=tui", "--ui-keep-open")
		err := executeCLI(t.Context(), args, cliIO{
			in:     cliNoInput{t},
			out:    io.Discard,
			errOut: io.Discard,
		})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("HDR route error = %v, want %s", err, test.want)
		}
	}
}

func TestLauncherCannotSwitchToHDRRoutes(t *testing.T) {
	for _, flags := range []string{"--hdr-analysis-only --hdr-output-dir=output", "--hdr-resume-workflow=retained-workflow"} {
		_, err := validateCLILaunch([]string{"--ui=tui"}, cliLaunchRequest{
			Sources:   []string{filepath.Join(t.TempDir(), "Synthetic.HDR.mkv")},
			Arguments: "--ui=tui " + flags,
			Changed:   true,
		})
		if err == nil {
			t.Fatal("launcher changed into an HDR command route")
		}
	}
}

func TestCompositeHDRReportsSurviveTerminalRestoration(t *testing.T) {
	for _, test := range []struct {
		name     string
		tui      bool
		canceled bool
		items    int
	}{
		{
			name:  "completed queue",
			tui:   true,
			items: 2,
		},
		{
			name:     "canceled partial attempt",
			tui:      true,
			canceled: true,
			items:    1,
		},
		{name: "ordinary output", items: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output cliSynchronizedOutput
			var identifiers []string
			err := runCLIPresentation(t.Context(), cliIO{
				in:     strings.NewReader(""),
				out:    &output,
				errOut: io.Discard,
			}, cliTerminalCapabilities{Width: 120, Height: 40}, test.tui, false, true, nil, nil, false, false,
				func(ctx context.Context, _, _ []string, streams cliIO) error {
					for index := range test.items {
						itemCtx := withCLIItemPresentation(ctx, streams, "Synthetic.HDR.mkv", test.items)
						waitCtx, cancel := context.WithCancel(itemCtx)
						workflow := fmt.Sprintf("workflow_%032x", index+1)
						attempt := fmt.Sprintf("attempt_%032x", index+1)
						artifact := fmt.Sprintf("artifact_%032x", index+1)
						identifiers = append(identifiers, workflow, attempt, artifact)
						operation := api.WorkflowOperationStatus{
							WorkflowID: api.WorkflowID(workflow),
							ID:         "operation-hdr",
							Operation:  api.OperationKindUploadExecute,
							Status:     api.StageStatusRunning,
						}
						latest := releaseworkflow.CommandResult{
							Workflow: api.ReleaseWorkflow{ID: operation.WorkflowID, Revision: 1},
							HDRAnalysis: &api.HDRAnalysisResult{
								AttemptID: attempt,
								Status:    api.StageStatusCompleted,
								Targets: []api.HDRAnalysisTargetResult{{Artifacts: []api.HDRTypedArtifact{{
									ID: api.PublicResourceID(artifact), Kind: api.HDRArtifactDolbyVisionL1Plot,
								}}}},
							},
						}
						service := &cliHDRResumeCoreFake{
							current:   latest,
							operation: operation,
						}
						service.operation.Status = api.StageStatusCompleted
						if test.canceled {
							service.current.HDRAnalysis.Status = api.StageStatusPartial
							cancel()
						}
						session := &cliWorkflowSession{
							core:           service,
							streams:        streams,
							progressWriter: io.Discard,
							logger:         api.NopLogger{},
						}
						_, waitErr := session.waitForCompositeUpload(waitCtx, releaseworkflow.CommandResult{Operation: &operation})
						cancel()
						if service.readContextErr != nil || service.readDeadline.IsZero() {
							t.Error("HDR outcome was not read with a detached bounded context")
						}
						publishCLIItemResult(streams, waitErr)
						if waitErr != nil {
							return waitErr
						}
					}
					return nil
				})
			if test.canceled && !errors.Is(err, context.Canceled) || !test.canceled && err != nil {
				t.Fatalf("HDR wait changed its terminal result: %v", err)
			}
			text := output.String()
			if test.tui {
				exit := strings.LastIndex(text, "\x1b[?1049l")
				if exit < 0 {
					t.Fatal("terminal was not restored")
				}
				text = text[exit:]
				for item := range test.items {
					if !strings.Contains(text, fmt.Sprintf("Item %d: HDR workflow:", item+1)) {
						t.Fatal("HDR report lost its queue item")
					}
				}
			}
			for _, id := range identifiers {
				if strings.Count(text, id) != 1 {
					t.Fatal("exact HDR continuation identifier was lost, redacted, or duplicated")
				}
			}
		})
	}
}
