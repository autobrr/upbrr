// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowRequestFingerprintsAreDeterministic(t *testing.T) {
	t.Parallel()

	first := ProjectReleaseWorkflowTrackersRequest{
		ReleaseWorkflowCommandContext: testWorkflowCommandContext(),
		TrackerIDs:                    []TrackerID{"ALPHA", "BETA"},
		Instructions: map[TrackerID]TrackerProjectionInstructions{
			"ALPHA": {},
			"BETA":  {},
		},
	}
	second := ProjectReleaseWorkflowTrackersRequest{
		ReleaseWorkflowCommandContext: testWorkflowCommandContext(),
		TrackerIDs:                    []TrackerID{"ALPHA", "BETA"},
		Instructions: map[TrackerID]TrackerProjectionInstructions{
			"BETA":  {},
			"ALPHA": {},
		},
	}
	firstFingerprint, err := CanonicalWorkflowFingerprint(first)
	if err != nil {
		t.Fatalf("fingerprint first request: %v", err)
	}
	secondFingerprint, err := CanonicalWorkflowFingerprint(second)
	if err != nil {
		t.Fatalf("fingerprint second request: %v", err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("map insertion order changed request fingerprint: %s != %s", firstFingerprint, secondFingerprint)
	}
}

func TestContinueWorkflowRequestRequiresTypedGoalAndAuthority(t *testing.T) {
	t.Parallel()

	begin := ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-1",
		Goal:           WorkflowGoalPrepared,
		Intent: WorkflowIntent{
			Preparation: &PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
		},
	}
	if err := begin.Validate(); err != nil {
		t.Fatalf("validate begin continuation: %v", err)
	}
	begin.Authority = &WorkflowAuthority{WorkflowID: "workflow-1"}
	if err := begin.Validate(); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("missing continuation revision error = %v", err)
	}
	begin.Authority.ExpectedRevision = 2
	begin.Intent.MediaSelection = &WorkflowMediaSelection{}
	if err := begin.Validate(); err == nil || !strings.Contains(err.Error(), "explicit media selection") {
		t.Fatalf("explicit empty media selection error = %v", err)
	}
	begin.Intent.MediaSelection = nil
	begin.Goal = WorkflowGoalPrepared
	begin.Intent.Interaction = "prompt_when_convenient"
	if err := begin.Validate(); err == nil || !strings.Contains(err.Error(), "interaction mode") {
		t.Fatalf("unknown continuation interaction error = %v", err)
	}
	begin.Intent.Interaction = InteractionModeUnattended
	begin.Intent.UploadTrackerIDs = []TrackerID{"alpha", "ALPHA"}
	if err := begin.Validate(); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate upload tracker error = %v", err)
	}
	begin.Intent.UploadTrackerIDs = nil
	begin.Goal = "adapter_owned_stage"
	if err := begin.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported workflow goal") {
		t.Fatalf("unknown continuation goal error = %v", err)
	}
}

func TestWorkflowRequestValidationRejectsMissingAndStaleAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request interface{ Validate() error }
		want    string
	}{
		{
			name:    "missing workflow",
			request: CancelReleaseWorkflowRequest{},
			want:    "workflow ID",
		},
		{
			name: "missing revision",
			request: CancelReleaseWorkflowRequest{ReleaseWorkflowCommandContext: ReleaseWorkflowCommandContext{
				WorkflowID:     "workflow-1",
				IdempotencyKey: "intent-1",
			}},
			want: "revision",
		},
		{
			name: "stale action revision",
			request: ResolveReleaseWorkflowActionRequest{
				ReleaseWorkflowCommandContext: testWorkflowCommandContext(),
				Answer:                        RequiredActionAnswer{ActionID: "action-1", WorkflowRevision: 6},
			},
			want: "does not match",
		},
		{
			name: "pathless attachment",
			request: AttachReleaseWorkflowMediaRequest{
				ReleaseWorkflowCommandContext: testWorkflowCommandContext(),
				Attachments:                   []MediaAttachment{{}},
			},
			want: "resource ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestWorkflowRequestJSONRoundTripClonesSlices(t *testing.T) {
	t.Parallel()

	original := UploadReleaseWorkflowImagesRequest{
		ReleaseWorkflowCommandContext: testWorkflowCommandContext(),
		Media:                         MediaArtifactSetRef{ID: "media-1", Revision: 4},
		ArtifactIDs:                   []PublicResourceID{"artifact-1", "artifact-2"},
		Host:                          "example-host",
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal workflow request: %v", err)
	}
	var cloned UploadReleaseWorkflowImagesRequest
	if err := json.Unmarshal(payload, &cloned); err != nil {
		t.Fatalf("unmarshal workflow request: %v", err)
	}
	cloned.ArtifactIDs[0] = "changed"
	if original.ArtifactIDs[0] != "artifact-1" {
		t.Fatalf("round-trip clone aliased original slice: %#v", original.ArtifactIDs)
	}
}

func TestNewWorkflowContractsDoNotSerializePrivatePathsOrTokens(t *testing.T) {
	t.Parallel()

	value := struct {
		Resource WorkflowResourceRef `json:"resource"`
		Plan     MediaPlan           `json:"plan"`
		DryRun   UploadDryRunResult  `json:"dryRun"`
	}{
		Resource: WorkflowResourceRef{
			ID:          "resource-1",
			ContentType: "image/png",
			SizeBytes:   42,
		},
		Plan: MediaPlan{
			ID:         "plan-1",
			WorkflowID: "workflow-1",
			Revision:   3,
		},
		DryRun: UploadDryRunResult{
			ID:         "dry-run-1",
			WorkflowID: "workflow-1",
			Revision:   4,
		},
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal safe workflow contracts: %v", err)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{"sourcepath", "localpath", "token", "credential", "cookie"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("safe workflow contracts serialized forbidden authority %q: %s", forbidden, payload)
		}
	}
}

func testWorkflowCommandContext() ReleaseWorkflowCommandContext {
	return ReleaseWorkflowCommandContext{
		WorkflowID:       "workflow-1",
		ExpectedRevision: 7,
		IdempotencyKey:   "intent-1",
	}
}
