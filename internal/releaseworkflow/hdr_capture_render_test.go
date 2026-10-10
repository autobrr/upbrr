// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRPlaylistCaptureAutomaticallyPublishesIncludedPlots(t *testing.T) {
	for _, test := range []struct {
		name               string
		capture, supported bool
		builds             int
	}{
		{
			name:      "confirmed capture",
			capture:   true,
			supported: true,
			builds:    1,
		},
		{name: "ordinary playlist preparation", supported: true},
		{name: "absent HDR metadata", capture: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := &hdrBuilderFixture{}
			preparer := testPreparer()
			target := api.HDRTargetID("Synthetic Disc", "00001.MPLS")
			preparer.DisplayFunc = func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				return api.PreparedReleaseDisplay{HDRTargets: []api.HDRAnalysisTarget{{
					ID:              target,
					Label:           "Disc 1",
					Playlist:        "00001.MPLS",
					SelectionPolicy: "primary_hevc_angle_zero",
					Supported:       test.supported,
				}}}, nil
			}
			module, _ := newTestModule(t, preparer, WithHDRAnalysisBuilder(builder))
			created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-capture"})
			command := PrepareReleaseCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "Synthetic Disc", Controls: api.PreparationControls{CaptureHDRMetadata: test.capture}},
				IdempotencyKey:   "capture",
			}
			prepared := executeCommand(t, module, command)
			if builder.builds != test.builds {
				t.Fatalf("builds=%d want=%d", builder.builds, test.builds)
			}
			if test.builds == 0 {
				if prepared.HDRAnalysis != nil || prepared.Workflow.HDRAnalysisEnabled {
					t.Fatal("non-HDR preparation published a plot")
				}
				return
			}
			if prepared.HDRAnalysis == nil || !prepared.Workflow.HDRAnalysisEnabled || prepared.HDRAnalysis.PeakSource != api.HDRPeakHistogram ||
				prepared.HDRAnalysis.Targets[0].Artifact == nil {
				t.Fatalf("captured metadata not rendered and included: %#v", prepared)
			}
			if _, err := module.HDRAnalysisArtifact(
				t.Context(),
				testOwnerID,
				prepared.Workflow.ID,
				*prepared.Workflow.HDRAnalysis,
				prepared.HDRAnalysis.Targets[0].Artifact.ID,
			); err != nil {
				t.Fatal(err)
			}
			replayed := executeCommand(t, module, command)
			if replayed.HDRAnalysis.ID != prepared.HDRAnalysis.ID || builder.builds != 1 {
				t.Fatal("playlist confirmation replay regenerated the image")
			}
		})
	}
}

func TestHDRPlaylistCaptureDefersRenderingToCompositePlanner(t *testing.T) {
	builder := &hdrBuilderFixture{}
	preparer := testPreparer()
	preparer.DisplayFunc = func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
		return api.PreparedReleaseDisplay{HDRTargets: []api.HDRAnalysisTarget{
			{
				ID: api.HDRTargetID(
					"Synthetic Disc",
					"00001.MPLS",
				),
				Label:           "Disc 1",
				Playlist:        "00001.MPLS",
				Supported:       true,
				SelectionPolicy: "primary_hevc_angle_zero",
			},
		}}, nil
	}
	module, repo := newTestModule(t, preparer, WithHDRAnalysisBuilder(builder))
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "hdr-composite"})
	state, err := repo.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Composite = &compositeUploadSession{Intent: api.WorkflowIntent{HDRAnalysis: &api.HDRAnalysisRequest{PeakSource: api.HDRPeakMaxSCL}}}
	_, err = module.prepareRelease(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, module.clock.Now(), PrepareReleaseCommand{
		Input: api.PrepareInput{SourcePath: "Synthetic Disc", Controls: api.PreparationControls{CaptureHDRMetadata: true}},
	})
	if err != nil || builder.builds != 0 {
		t.Fatalf("preparation bypassed the composite estimator/target planner: builds=%d err=%v", builder.builds, err)
	}
}
