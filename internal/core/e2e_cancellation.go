// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// e2eWaitForWorkflowStage holds a synthetic service at a test-owned HTTP gate.
// The real mkbrr cancellation and cleanup behavior is exercised in torrent tests.
func e2eWaitForWorkflowStage(ctx context.Context, stage string) error {
	if !isE2EEnabled() || os.Getenv("UPBRR_E2E_CANCELLATION_STAGE") != stage {
		return nil
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		Task:            stage,
		Status:          "running",
		Message:         "Synthetic " + stage + " work active.",
		CompletedPieces: 0,
		TotalPieces:     2,
	})
	endpoint := strings.TrimRight(os.Getenv(e2eTrackerURLEnv), "/") + "/workflow-stage/" + stage
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("e2e workflow gate request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("e2e workflow gate wait: %w", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("e2e workflow gate status: %d", response.StatusCode)
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		Task:            stage,
		Status:          "running",
		Message:         "Synthetic " + stage + " work finished.",
		CompletedPieces: 2,
		TotalPieces:     2,
	})
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("e2e workflow gate canceled at return boundary: %w", err)
	}
	return nil
}
