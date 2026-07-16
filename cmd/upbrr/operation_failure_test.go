// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
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
