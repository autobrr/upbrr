// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCommandFromRequestRejectsInvalidSharedRequest(t *testing.T) {
	t.Parallel()

	_, err := CommandFromRequest(api.UploadReleaseWorkflowRequest{})
	if err == nil || !strings.Contains(err.Error(), "workflow ID is required") {
		t.Fatalf("error = %v, want workflow authority validation failure", err)
	}
}

func TestCommandFromRequestMapsDirectUploadExactly(t *testing.T) {
	t.Parallel()

	request := api.UploadReleaseWorkflowRequest{
		WorkflowID:       api.WorkflowID("workflow-1"),
		ExpectedRevision: 7,
		IdempotencyKey:   "upload-1",
		NoSeed:           true,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
	}

	mapped, err := CommandFromRequest(request)
	if err != nil {
		t.Fatalf("map direct upload request: %v", err)
	}
	command, ok := mapped.(ExecuteUploadsCommand)
	if !ok {
		t.Fatalf("command type = %T, want ExecuteUploadsCommand", mapped)
	}
	if command.WorkflowID != request.WorkflowID || command.ExpectedRevision != request.ExpectedRevision ||
		command.IdempotencyKey != request.IdempotencyKey || command.NoSeed != request.NoSeed ||
		!slices.Equal(command.TrackerIDs, request.TrackerIDs) {
		t.Fatalf("command = %#v, request = %#v", command, request)
	}
}

func TestCommandFromRequestMapsAudioAnalysisAndDurableEnabledIntent(t *testing.T) {
	t.Parallel()

	context := api.ReleaseWorkflowCommandContext{
WorkflowID: "workflow-1",
 ExpectedRevision: 7,
 IdempotencyKey: "audio-1",
}
	instructions := api.AudioAnalysisInstructions{
 Release: api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 2},
		ResourceID: "resource-1",
 Selection: api.AudioAnalysisSelectionSelected,
 TrackIDs: []string{"track-1"},
		Variants: []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
 ProfileVersion: api.AudioAnalysisProfileVersion,
	}
	mapped, err := CommandFromRequest(api.AnalyzeReleaseWorkflowAudioRequest{
		ReleaseWorkflowCommandContext: context,
		Instructions:                  instructions,
	})
	if err != nil {
		t.Fatal(err)
	}
	analysis, ok := mapped.(AnalyzeAudioCommand)
	if !ok || analysis.WorkflowID != context.WorkflowID || analysis.ExpectedRevision != context.ExpectedRevision ||
		analysis.IdempotencyKey != context.IdempotencyKey || !slices.Equal(analysis.Instructions.TrackIDs, instructions.TrackIDs) {
		t.Fatalf("mapped analysis command = %#v", mapped)
	}

	mapped, err = CommandFromRequest(api.SetReleaseWorkflowAudioAnalysisEnabledRequest{
		ReleaseWorkflowCommandContext: context,
		Enabled:                       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled, ok := mapped.(SetAudioAnalysisEnabledCommand)
	if !ok || enabled.WorkflowID != context.WorkflowID || enabled.ExpectedRevision != context.ExpectedRevision || enabled.Enabled {
		t.Fatalf("mapped enabled command = %#v", mapped)
	}
}

func TestCommandFromRequestRejectsUnsupportedRequest(t *testing.T) {
	t.Parallel()

	_, err := CommandFromRequest(api.GetReleaseWorkflowRequest{WorkflowID: api.WorkflowID("workflow-1")})
	if err == nil || !strings.Contains(err.Error(), "unsupported release workflow request") {
		t.Fatalf("error = %v, want unsupported request failure", err)
	}
}
