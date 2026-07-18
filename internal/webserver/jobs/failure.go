// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import "github.com/autobrr/upbrr/pkg/api"

// failureForError preserves a structured operation failure as a defensive copy
// and otherwise creates a frontend-safe generic failure.
func failureForError(err error, operation api.OperationKind, message string, recovery api.OperationRecovery) *api.OperationFailure {
	if failure, ok := api.AsOperationFailure(err); ok {
		cloned := failure
		return &cloned
	}
	return genericFailure(operation, message, recovery)
}

func genericFailure(operation api.OperationKind, message string, recovery api.OperationRecovery) *api.OperationFailure {
	return &api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: operation,
		Message:   message,
		Recovery:  recovery,
	}
}

func cloneFailure(failure *api.OperationFailure) *api.OperationFailure {
	if failure == nil {
		return nil
	}
	cloned := *failure
	return &cloned
}
