// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWriteAppErrorBDMVRescanRequiredIsStructuredAndPathFree(t *testing.T) {
	recorder := httptest.NewRecorder()

	cause := &api.BDMVRescanRequiredError{
		SourcePath:        `/disc`,
		SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		CachedPlaylists:   []string{"00001.MPLS"},
		MissingPlaylists:  []string{"00002.MPLS"},
	}
	writeAppError(recorder, api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureConfirmationRequired,
		Operation: api.OperationKindPreparation,
		Message:   "Blu-ray playlist changes require confirmation before rescanning.",
		Recovery:  api.OperationRecoveryConfirm,
	}, cause))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", recorder.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	failure, ok := payload["failure"].(map[string]any)
	if !ok {
		t.Fatalf("missing failure: %#v", payload)
	}
	if got := failure["Code"]; got != string(api.OperationFailureConfirmationRequired) {
		t.Fatalf("failure code = %#v", got)
	}
	if _, exists := payload["source_path"]; exists {
		t.Fatalf("response exposed source path: %#v", payload)
	}
}

func TestWriteAppErrorUntypedFallbackIsStructuredAndSafe(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeAppError(recorder, errors.New(`token=secret-value path=C:\private\release`))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret-value") || strings.Contains(recorder.Body.String(), `C:\private`) {
		t.Fatalf("unsafe response: %s", recorder.Body.String())
	}
}

func TestWriteAppErrorHDRFailureKeepsCodeAndRecovery(t *testing.T) {
	t.Parallel()
	for _, code := range []api.HDRAnalysisFailureCode{api.HDRAnalysisFailureRead, api.HDRAnalysisFailureOutput} {
		t.Run(string(code), func(t *testing.T) {
			// Exercise JSON backslash escaping on every platform.
			cause := &os.PathError{
				Op:   "mkdir",
				Path: filepath.Join(t.TempDir(), `private\output`),
				Err:  os.ErrPermission,
			}
			failure := api.OperationFailure{
				Code:            api.OperationFailureHDRAnalysis,
				Operation:       api.OperationKindHDRAnalysis,
				Message:         "Synthetic HDR operation failed.",
				Recovery:        api.OperationRecoveryRetry,
				HDRAnalysisCode: code,
			}
			recorder := httptest.NewRecorder()
			writeAppError(recorder, api.NewOperationError(failure, cause))
			var payload struct {
				Error   string               `json:"error"`
				Failure api.OperationFailure `json:"failure"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusConflict || payload.Error != failure.Message || payload.Failure != failure {
				t.Fatalf("HDR failure response changed: status=%d payload=%#v", recorder.Code, payload)
			}
			body := recorder.Body.String()
			escapedPath := strings.ReplaceAll(cause.Path, `\`, `\\`)
			if strings.Contains(body, cause.Path) || strings.Contains(body, escapedPath) {
				t.Fatal("HDR failure response exposed its private filesystem cause")
			}
		})
	}
}
