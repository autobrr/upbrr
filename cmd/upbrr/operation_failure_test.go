// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestFormatCLIErrorUsesStableOperationFailure(t *testing.T) {
	err := api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureConfirmationRequired,
		Operation: api.OperationKindPreparation,
		Message:   "Blu-ray playlist changes require confirmation before rescanning.",
		Recovery:  api.OperationRecoveryConfirm,
	}, errors.New("private database path and token"))

	if got, want := formatCLIError(err), "Blu-ray playlist changes require confirmation before rescanning. (recovery: confirm)"; got != want {
		t.Fatalf("formatCLIError() = %q, want %q", got, want)
	}
}

func TestPrintCLIErrorPreservesCleanupFailures(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private.db")
	for _, scenario := range []struct {
		name        string
		shutdownErr error
		closeErr    error
		wantCleanup string
	}{
		{
			name:        "shutdown",
			shutdownErr: errors.New("synthetic worker token=cleanup-secret"),
			wantCleanup: "shutdown could not confirm all work stopped; inspect retained state before retrying; repository remains open: synthetic worker token=[REDACTED]",
		},
		{
			name:        "repository close",
			closeErr:    fmt.Errorf("synthetic close token=cleanup-secret at %s", privatePath),
			wantCleanup: "close workflow repository: synthetic close token=[REDACTED] at " + privatePath,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			operationErr := api.NewOperationError(api.OperationFailure{
				Code:      api.OperationFailureInternal,
				Operation: api.OperationKindUploadExecute,
				Message:   "Upload failed.",
				Recovery:  api.OperationRecoveryRetry,
			}, errors.Join(errors.New("private operation cause"), errors.New("private joined cause")))
			core := &cliWorkflowCoreLifecycleFake{shutdownErr: scenario.shutdownErr, closeErr: scenario.closeErr}
			cleanupErr := closeCLIWorkflowCore(t.Context(), core)
			err := errors.Join(exitError(1, operationErr), cleanupErr)
			var output strings.Builder
			printCLIError(&output, err)
			want := "error: Upload failed. (recovery: retry)\n" + scenario.wantCleanup + "\n"
			if got := output.String(); got != want {
				t.Fatalf("printCLIError() = %q, want %q", got, want)
			}
			if !errors.Is(err, operationErr) || !errors.Is(err, cleanupErr) {
				t.Fatal("combined error lost operation or cleanup identity")
			}
		})
	}
}

func TestFormatCLIErrorPreservesWrappedJoinedFailures(t *testing.T) {
	operationErr := api.NewOperationError(api.OperationFailure{
		Message:  "Upload failed.",
		Recovery: api.OperationRecoveryRetry,
	}, errors.New("private operation cause"))
	err := exitError(1, fmt.Errorf("workflow: %w", errors.Join(
		errors.New("first cleanup failed"),
		errors.Join(operationErr, errors.New("second cleanup failed")),
	)))
	want := "first cleanup failed\nUpload failed. (recovery: retry)\nsecond cleanup failed"
	if got := formatCLIError(err); got != want {
		t.Fatalf("formatCLIError() = %q, want %q", got, want)
	}
}

func TestFormatCLIErrorPreservesUnstructuredContext(t *testing.T) {
	err := fmt.Errorf("cleanup: %w; repository remains open: %w", errors.New("shutdown failed"), errors.New("worker failed"))
	if got, want := formatCLIError(err), "cleanup: shutdown failed; repository remains open: worker failed"; got != want {
		t.Fatalf("formatCLIError() = %q, want %q", got, want)
	}
}
