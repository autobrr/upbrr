// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"
	"testing"

	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestClassifyOperationErrorCanonicalMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation api.OperationKind
		cause     error
		wantCode  api.OperationFailureCode
		message   string
		recovery  api.OperationRecovery
	}{
		{
name: "missing source",
 operation: api.OperationKindPreparation,
 cause: api.ErrPreparationSourceRequired,
 wantCode: api.OperationFailureInvalidSource,
 message: "A source path is required.",
 recovery: api.OperationRecoveryEditInput,
},
		{
name: "unavailable source",
 operation: api.OperationKindPreparation,
 cause: sourcelayout.ErrSourceNotFound,
 wantCode: api.OperationFailureInvalidSource,
 message: "The source path is unavailable.",
 recovery: api.OperationRecoveryEditInput,
},
		{
name: "playlist required",
 operation: api.OperationKindPreparation,
 cause: &api.PlaylistSelectionRequiredError{SourcePath: `C:\private`},
 wantCode: api.OperationFailureMissingPrerequisite,
 message: "Select at least one Blu-ray playlist before preparing the release.",
 recovery: api.OperationRecoveryCompletePrerequisite,
},
		{
name: "playlist invalid",
 operation: api.OperationKindPreparation,
 cause: &api.InvalidPlaylistSelectionError{SourcePath: `C:\private`, Playlist: "../bad.mpls"},
 wantCode: api.OperationFailureInvalidInput,
 message: "The Blu-ray playlist selection is invalid.",
 recovery: api.OperationRecoveryEditInput,
},
		{
name: "rescan confirmation",
 operation: api.OperationKindPreparation,
 cause: &api.BDMVRescanRequiredError{SourcePath: `C:\private`},
 wantCode: api.OperationFailureConfirmationRequired,
 message: "Blu-ray playlist changes require confirmation before rescanning.",
 recovery: api.OperationRecoveryConfirm,
},
		{
name: "client failure",
 operation: api.OperationKindDuplicateCheck,
 cause: errors.New("client secret and private path"),
 wantCode: api.OperationFailureInternal,
 message: "The operation could not be completed.",
 recovery: api.OperationRecoveryRetry,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, ok := api.AsOperationFailure(classifyOperationError(test.operation, test.cause))
			if !ok {
				t.Fatal("classified error has no operation failure")
			}
			want := api.OperationFailure{
Code: test.wantCode,
 Operation: test.operation,
 Message: test.message,
 Recovery: test.recovery,
}
			if failure != want {
				t.Fatalf("failure = %#v, want %#v", failure, want)
			}
		})
	}
}
