// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "errors"

// OperationKind identifies the domain operation that failed.
type OperationKind string

const (
	OperationKindUnknown        OperationKind = "unknown"
	OperationKindPreparation    OperationKind = "preparation"
	OperationKindDuplicateCheck OperationKind = "duplicate_check"
	OperationKindDryRun         OperationKind = "dry_run"
	OperationKindUploadReview   OperationKind = "upload_review"
	OperationKindUploadExecute  OperationKind = "upload_execute"
	OperationKindMedia          OperationKind = "media"
	OperationKindDescription    OperationKind = "description"
	OperationKindImageHosting   OperationKind = "image_hosting"
)

// OperationFailureCode is a stable machine-readable failure classification.
type OperationFailureCode string

const (
	OperationFailureInvalidInput           OperationFailureCode = "invalid_input"
	OperationFailureInvalidSource          OperationFailureCode = "invalid_source"
	OperationFailureConfirmationRequired   OperationFailureCode = "confirmation_required"
	OperationFailureStaleGeneration        OperationFailureCode = "stale_generation"
	OperationFailureIncompatibleGeneration OperationFailureCode = "incompatible_generation"
	OperationFailureMissingPrerequisite    OperationFailureCode = "missing_prerequisite"
	OperationFailureTrackerAuthRequired    OperationFailureCode = "tracker_auth_required"
	OperationFailureNoEligibleTrackers     OperationFailureCode = "no_eligible_trackers"
	OperationFailureStaleReview            OperationFailureCode = "stale_review"
	OperationFailureMissingReview          OperationFailureCode = "missing_review"
	OperationFailureInternal               OperationFailureCode = "internal"
)

// OperationRecovery is the required caller action for a stable failure.
type OperationRecovery string

const (
	OperationRecoveryNone                 OperationRecovery = "none"
	OperationRecoveryEditInput            OperationRecovery = "edit_input"
	OperationRecoveryConfirm              OperationRecovery = "confirm"
	OperationRecoveryRefreshRelease       OperationRecovery = "refresh_release"
	OperationRecoveryCompletePrerequisite OperationRecovery = "complete_prerequisite"
	OperationRecoveryAuthenticateTrackers OperationRecovery = "authenticate_trackers"
	OperationRecoverySelectTrackers       OperationRecovery = "select_trackers"
	OperationRecoveryReviewAgain          OperationRecovery = "review_again"
	OperationRecoveryRetry                OperationRecovery = "retry"
)

// OperationFailure is safe for CLI and browser transport. Raw causes are
// intentionally excluded.
type OperationFailure struct {
	Code      OperationFailureCode
	Operation OperationKind
	Message   string
	Recovery  OperationRecovery
}

// OperationError preserves one wrapped cause while exposing only its safe
// failure through Error and Failure.
type OperationError struct {
	failure OperationFailure
	cause   error
}

// NewOperationError creates a typed safe operation error.
func NewOperationError(failure OperationFailure, cause error) *OperationError {
	if cause == nil {
		cause = errors.New("operation failed")
	}
	return &OperationError{failure: failure, cause: cause}
}

func (e *OperationError) Error() string {
	if e == nil {
		return "operation failed"
	}
	return e.failure.Message
}

// Unwrap retains errors.Is/errors.As access to the private cause.
func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Failure returns the stable safe transport value.
func (e *OperationError) Failure() OperationFailure {
	if e == nil {
		return OperationFailure{}
	}
	return e.failure
}

// AsOperationFailure extracts a stable failure from a wrapped error chain.
func AsOperationFailure(err error) (OperationFailure, bool) {
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		return OperationFailure{}, false
	}
	return operationErr.Failure(), true
}
