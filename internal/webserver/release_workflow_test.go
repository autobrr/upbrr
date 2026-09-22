// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
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
		{"unresolved effect", fmt.Errorf("reserve input: %w", api.ErrReleaseWorkflowEffectOutcomeUnknown), api.OperationFailureUnknownOutcome, api.OperationRecoveryConfirm},
		{"active busy", api.ErrActiveInputBusy, api.OperationFailureActiveInputBusy, api.OperationRecoveryReviewAgain},
		{"active changed", api.ErrActiveInputChanged, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
		{"active lease lost", api.ErrActiveInputLeaseLost, api.OperationFailureStaleReview, api.OperationRecoveryReviewAgain},
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

func TestReleaseWorkflowAppAudioAnalysisArtifactUsesAuthenticatedSessionAuthority(t *testing.T) {
	t.Parallel()

	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	coreFake := &audioAnalysisArtifactCoreFake{expectedOwner: current.ID}
	server.backend.replaceRuntime(config.Config{}, CoreCapabilities{ReleaseWorkflow: coreFake}, nil)
	mux := http.NewServeMux()
	server.registerReleaseWorkflowAppRoutes(mux)
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet,
		"/api/app/release-workflow-audio-analysis?workflowId=workflow-1&analysisId=analysis-1&analysisRevision=4&artifactId=artifact-1", nil,
	)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "synthetic-png" ||
		coreFake.owner != current.ID || coreFake.analysis.Revision != 4 || coreFake.artifactID != "artifact-1" {
		t.Fatalf("app artifact status=%d owner=%q analysis=%#v artifact=%q body=%q", response.Code, coreFake.owner, coreFake.analysis, coreFake.artifactID, response.Body.String())
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "private, no-store" ||
		response.Header().Get("Content-Disposition") != `inline; filename="audio-analysis.png"` {
		t.Fatalf("app artifact headers = %v", response.Header())
	}

	unauthorized := httptest.NewRecorder()
	statsRequest := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet,
		"/api/app/release-workflow-audio-analysis?workflowId=workflow-1&analysisId=analysis-1&analysisRevision=4&artifactId=stats-1", nil,
	)
	statsRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	statsResponse := httptest.NewRecorder()
	mux.ServeHTTP(statsResponse, statsRequest)
	if statsResponse.Code != http.StatusOK || statsResponse.Body.String() != "DC offset   0.000000\n" ||
		statsResponse.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		statsResponse.Header().Get("Content-Disposition") != `inline; filename="audio-analysis-stats.txt"` {
		t.Fatalf("statistics response status=%d headers=%v body=%q", statsResponse.Code, statsResponse.Header(), statsResponse.Body.String())
	}
	request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/release-workflow-audio-analysis", nil)
	mux.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized app artifact status = %d", unauthorized.Code)
	}
}
