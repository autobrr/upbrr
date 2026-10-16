// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliHDRResumeCoreFake struct {
	cliWorkflowCoreFake
	cancel         context.CancelFunc
	readErr        error
	readContextErr error
	readDeadline   time.Time
}

func TestCLIHDRResumePrefersPendingRequestWithoutReconstructingOptions(t *testing.T) {
	for _, olderResult := range []bool{false, true} {
		t.Run(map[bool]string{false: "first attempt", true: "newer interrupted attempt"}[olderResult], func(t *testing.T) {
			request := api.HDRAnalysisInstructions{
				Release:        api.ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 2},
				TargetIDs:      []string{"hdr_0123456789abcdef0123456789abcdef"},
				PeakSource:     api.HDRPeakMaxSCLLuminance,
				ProfileVersion: api.HDRAnalysisProfileV2,
				Formats:        api.HDRAnalysisFormatsBoth,
				Decoded:        &api.HDRDecodedOptions{SceneSync: true},
			}
			current := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{ID: "pending-workflow", Revision: 4},
				Release: &api.ReleaseSnapshot{
					Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: request.Release.SourcePath}, Generation: 2},
				},
				HDRPendingRequest: &request,
			}
			if olderResult {
				current.HDRAnalysis = &api.HDRAnalysisResult{
					Release:    request.Release,
					PeakSource: api.HDRPeakHistogram,
					AttemptID:  "older",
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service := &cliHDRResumeCoreFake{
				current:        current,
				activeInput:    api.ActiveInputSnapshot{State: api.ActiveInputActive, Current: &current},
				queueOperation: true,
				cancel:         cancel,
			}
			var output bytes.Buffer
			if err := runCLIHDRResume(
				ctx,
				service,
				cliOptions{HDRResumeWorkflow: "pending-workflow"},
				cliIO{out: &output},
				api.NopLogger{},
			); !errors.Is(
				err,
				context.Canceled,
			) {
				t.Fatalf("pending request did not start: %v", err)
			}
			if len(service.commands) != 1 {
				t.Fatalf("commands=%v", service.commands)
			}
			command, ok := service.commands[0].(releaseworkflow.AnalyzeHDRCommand)
			if !ok || !reflect.DeepEqual(command.Instructions, request) {
				t.Fatalf("resumed different options: %+v", command)
			}
			stale := current
			stale.Release = &api.ReleaseSnapshot{
				Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: request.Release.SourcePath}, Generation: 3},
			}
			if _, err := cliHDRResumeInstructions(stale, cliOptions{}); err == nil {
				t.Fatal("resumed a different generation")
			}
			if _, err := cliHDRResumeInstructions(current, cliOptions{HDRRetryModes: "scene_sync", HDRRetryAttempt: "older"}); err == nil {
				t.Fatal("pending request permitted a second retry grant")
			}
		})
	}
}

func (f *cliHDRResumeCoreFake) StartReleaseWorkflow(ctx context.Context, owner string, command releaseworkflow.Command) (api.WorkflowOperationStatus, error) {
	operation, err := f.cliWorkflowCoreFake.StartReleaseWorkflow(ctx, owner, command)
	f.cancel()
	return operation, err
}

func (f *cliHDRResumeCoreFake) CurrentReleaseWorkflow(ctx context.Context, _ string, _ api.WorkflowID) (releaseworkflow.CommandResult, error) {
	f.readContextErr = ctx.Err()
	f.readDeadline, _ = ctx.Deadline()
	return f.current, f.readErr
}

func TestCLIHDRResumeCancellationRetainsArtifactOutput(t *testing.T) {
	for _, readFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained artifacts", true: "outcome read failure"}[readFails], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			current := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{ID: "workflow-hdr-resume", Revision: 1},
				Release: &api.ReleaseSnapshot{Release: api.PreparedRelease{
					Source:     api.SourceManifest{SourcePath: "Example.Release.2026.2160p-GRP.mkv"},
					Generation: 1,
				}},
				HDRAnalysis: &api.HDRAnalysisResult{
					Release:   api.ReleaseRef{SourcePath: "Example.Release.2026.2160p-GRP.mkv", Generation: 1},
					TargetIDs: []string{"hdr_0123456789abcdef0123456789abcdef"},
					AttemptID: "attempt-retained",
					Status:    api.StageStatusPartial,
					Targets: []api.HDRAnalysisTargetResult{{Artifacts: []api.HDRTypedArtifact{{
						ID: "artifact-retained", Kind: api.HDRArtifactDolbyVisionL1Plot,
					}}}},
				},
			}
			service := &cliHDRResumeCoreFake{
				current:        current,
				activeInput:    api.ActiveInputSnapshot{State: api.ActiveInputActive, Current: &current},
				queueOperation: true,
				cancel:         cancel,
			}
			if readFails {
				service.readErr = errors.New("outcome unavailable")
			}
			var output bytes.Buffer
			started := time.Now()
			err := runCLIHDRResume(ctx, service, cliOptions{HDRResumeWorkflow: string(current.Workflow.ID)}, cliIO{out: &output}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("resume error = %v, want cancellation preserved", err)
			}
			if service.cancelCalls != 1 {
				t.Fatalf("cancel calls = %d, want 1", service.cancelCalls)
			}
			if service.readContextErr != nil || service.readDeadline.Before(started) || service.readDeadline.After(time.Now().Add(5*time.Second)) {
				t.Fatalf("outcome read context error = %v, deadline = %v", service.readContextErr, service.readDeadline)
			}
			if got := strings.Contains(output.String(), "HDR artifact: artifact-retained"); got == readFails {
				t.Fatalf("artifact output present = %v, read failure = %v: %s", got, readFails, output.String())
			}
		})
	}
}
