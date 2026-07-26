// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/uploadinput"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveAPIV1UploadInputs(
	ctx context.Context,
	request api.CreateReleaseWorkflowUploadRequest,
) (context.Context, api.CreateReleaseWorkflowUploadRequest, error) {
	resolvedContext, resolved, err := uploadinput.Resolve(ctx, request)
	if err != nil {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, fmt.Errorf("resolve API v1 upload inputs: %w", err)
	}
	return resolvedContext, resolved, nil
}
