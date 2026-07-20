// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"testing"
)

func TestOperationErrorExposesSafeFailureAndRetainsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("token=secret-value")
	failure := OperationFailure{
		Code:      OperationFailureNoEligibleTrackers,
		Operation: OperationKindUploadReview,
		Message:   "No selected trackers are eligible.",
		Recovery:  OperationRecoverySelectTrackers,
	}
	err := fmt.Errorf("core boundary: %w", NewOperationError(failure, cause))
	if err.Error() != "core boundary: No selected trackers are eligible." {
		t.Fatalf("safe error = %q", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause was not retained")
	}
	actual, ok := AsOperationFailure(err)
	if !ok || actual != failure {
		t.Fatalf("failure = %#v, %v", actual, ok)
	}
}
