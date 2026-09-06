// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// LiveTestEnabled reports process policy without exposing mutable configuration.
func (c *Core) LiveTestEnabled() bool { return c.liveTest != nil }

// StartLiveTestReleaseWorkflowUpload drives ordinary rule evaluation to dry-run.
// This in-process CLI entrypoint is intentionally absent from the public API.
func (c *Core) StartLiveTestReleaseWorkflowUpload(
	ctx context.Context,
	ownerID string,
	request api.CreateReleaseWorkflowUploadRequest,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.StartLiveTestUpload(ctx, ownerID, request)
	return result, classifyOperationError(api.OperationKindUploadDryRun, err)
}
