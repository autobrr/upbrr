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
		ReleaseWorkflowCommandContext: api.ReleaseWorkflowCommandContext{
			WorkflowID:       api.WorkflowID("workflow-1"),
			ExpectedRevision: 7,
			IdempotencyKey:   "upload-1",
		},
		NoSeed:     true,
		TrackerIDs: []api.TrackerID{"ALPHA"},
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

func TestCommandFromRequestRejectsUnsupportedRequest(t *testing.T) {
	t.Parallel()

	_, err := CommandFromRequest(api.GetReleaseWorkflowRequest{WorkflowID: api.WorkflowID("workflow-1")})
	if err == nil || !strings.Contains(err.Error(), "unsupported release workflow request") {
		t.Fatalf("error = %v, want unsupported request failure", err)
	}
}
