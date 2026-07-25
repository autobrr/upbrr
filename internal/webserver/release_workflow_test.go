// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseWorkflowDiagnosticMessageExposesSanitizedCause(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf(
		`prepare source=D:\media\Example.Release.2026.1080p-GRP.mkv api_token=secret-value: %w`,
		errors.New("metadata probe failed"),
	)
	err := api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: api.OperationKindPreparation,
		Message:   "The operation could not be completed.",
		Recovery:  api.OperationRecoveryRetry,
	}, cause)

	message := releaseWorkflowDiagnosticMessage(err)
	for _, leaked := range []string{`D:\media`, "Example.Release.2026.1080p-GRP", "secret-value"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("diagnostic leaked protected value")
		}
	}
	for _, expected := range []string{"prepare source=[local path]", "api_token=[REDACTED]", "metadata probe failed"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("diagnostic %q missing %q", message, expected)
		}
	}
}

func TestClassifyReleaseWorkflowError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		code     api.OperationFailureCode
		recovery api.OperationRecovery
	}{
		{"missing", releaseworkflow.ErrWorkflowNotFound, api.OperationFailureMissingPrerequisite, api.OperationRecoveryRefreshRelease},
		{"revision", releaseworkflow.ErrRevisionConflict, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
		{"idempotency", releaseworkflow.ErrIdempotencyConflict, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
		{"transition", releaseworkflow.ErrInvalidTransition, api.OperationFailureMissingPrerequisite, api.OperationRecoveryCompletePrerequisite},
		{"private unavailable", releaseworkflow.ErrPrivateResourceUnavailable, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
		{"private consumed", releaseworkflow.ErrPrivateResourceConsumed, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var operationError *api.OperationError
			if !errors.As(classifyReleaseWorkflowError(test.err), &operationError) {
				t.Fatal("expected structured operation error")
			}
			failure := operationError.Failure()
			if failure.Code != test.code || failure.Recovery != test.recovery {
				t.Fatalf("failure = %#v, want code=%s recovery=%s", failure, test.code, test.recovery)
			}
		})
	}
}

func TestRetiredReleaseWorkflowAppStageRoutesAreNotRegistered(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	(&Server{}).registerReleaseWorkflowAppRoutes(mux)
	for _, method := range []string{
		"CreateReleaseWorkflow",
		"ReplaceReleaseWorkflowFacts",
		"PrepareReleaseWorkflow",
		"ResetReleaseWorkflow",
		"SelectReleaseWorkflowCandidate",
		"ProjectReleaseWorkflowTrackers",
		"PreflightReleaseWorkflowTrackers",
		"CheckReleaseWorkflowDuplicates",
		"DecideReleaseWorkflowDuplicates",
		"CaptureReleaseWorkflowMedia",
		"GenerateReleaseWorkflowDescriptions",
		"DryRunReleaseWorkflow",
		"UploadReleaseWorkflow",
		"ResolveReleaseWorkflowAction",
	} {
		request := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/api/app/"+method,
			strings.NewReader(`{}`),
		)
		if _, pattern := mux.Handler(request); pattern != "" {
			t.Fatalf("retired app stage route %s remains registered as %s", method, pattern)
		}
	}
}
