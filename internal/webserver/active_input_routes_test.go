// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type activeInputRouteFake struct {
	ReleaseWorkflowCapability
	get        api.ActiveInputSnapshot
	openErr    error
	recovered  api.RecoverLegacyActiveInputRequest
	reconciled api.ReconcileActiveInputRequest
	calls      int
}

func (f *activeInputRouteFake) GetActiveInput(context.Context, string) (api.ActiveInputSnapshot, error) {
	return f.get, nil
}

func (f *activeInputRouteFake) OpenActiveInput(context.Context, string, api.OpenActiveInputRequest) (api.ActiveInputSnapshot, error) {
	f.calls++
	return api.ActiveInputSnapshot{}, f.openErr
}

func (f *activeInputRouteFake) ReleaseActiveInput(context.Context, string, api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error) {
	f.calls++
	return api.ActiveInputSnapshot{}, nil
}

func (f *activeInputRouteFake) RecoverLegacyActiveInput(
	_ context.Context,
	_ string,
	request api.RecoverLegacyActiveInputRequest,
) (api.ActiveInputSnapshot, error) {
	f.calls++
	f.recovered = request
	return api.ActiveInputSnapshot{State: api.ActiveInputRecovering, Revision: 2}, nil
}

func (f *activeInputRouteFake) ReconcileActiveInput(
	_ context.Context,
	_ string,
	request api.ReconcileActiveInputRequest,
) (api.ActiveInputSnapshot, error) {
	f.calls++
	f.reconciled = request
	return api.ActiveInputSnapshot{State: api.ActiveInputEmpty, Revision: 3}, nil
}

func TestActiveInputLegacyRecoveryRoutes(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	capability := &activeInputRouteFake{get: api.ActiveInputSnapshot{
		State:               api.ActiveInputEmpty,
		Revision:            1,
		RecoveryWorkflowIDs: []api.WorkflowID{"legacy-workflow"},
	}}
	server.backend.replaceRuntime(config.Config{}, CoreCapabilities{ReleaseWorkflow: capability}, nil)
	mux := http.NewServeMux()
	server.registerActiveInputRoutes(mux)
	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/app/GetActiveInput", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	getResponse := httptest.NewRecorder()
	mux.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("legacy discovery status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	var discovered api.ActiveInputSnapshot
	if err := json.NewDecoder(getResponse.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	if len(discovered.RecoveryWorkflowIDs) != 1 || discovered.RecoveryWorkflowIDs[0] != "legacy-workflow" {
		t.Fatalf("legacy discovery = %#v", discovered)
	}

	recoverResponse := serveLegacyRecoveryRequest(t.Context(), t, mux, current, "/api/app/RecoverLegacyActiveInput", `{"workflowId":"legacy-workflow"}`)
	if recoverResponse.Code != http.StatusOK || capability.recovered.WorkflowID != "legacy-workflow" {
		t.Fatalf("legacy recover status=%d request=%#v body=%s", recoverResponse.Code, capability.recovered, recoverResponse.Body.String())
	}
	reconcileResponse := serveLegacyRecoveryRequest(t.Context(), t, mux, current, "/api/app/ReconcileActiveInput", `{
		"authority":{"workflowId":"legacy-workflow","expectedRevision":2},
		"answer":{"actionId":"action-1","workflowRevision":2,"selectedValues":["not_completed"]},
		"idempotencyKey":"resolve-1"
	}`)
	if reconcileResponse.Code != http.StatusOK || capability.reconciled.Authority.WorkflowID != "legacy-workflow" ||
		capability.reconciled.Answer.ActionID != "action-1" {
		t.Fatalf("legacy reconcile status=%d request=%#v body=%s", reconcileResponse.Code, capability.reconciled, reconcileResponse.Body.String())
	}
}

func TestActiveInputOpenReportsUnresolvedEffectWithoutPrivateDetails(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
	logger, err := logging.New(config.LoggingConfig{Level: "debug"}, filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	capability := &activeInputRouteFake{
		get: api.ActiveInputSnapshot{State: api.ActiveInputEmpty},
		openErr: fmt.Errorf(`reserve input source=D:\private\Example.mkv api_token=secret-value: %w`,
			api.ErrReleaseWorkflowEffectOutcomeUnknown),
	}
	server.backend.replaceRuntime(config.Config{}, CoreCapabilities{ReleaseWorkflow: capability}, logger)
	_, entries := logger.Subscribe(8)
	mux := http.NewServeMux()
	server.registerActiveInputRoutes(mux)
	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/app/GetActiveInput", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	getResponse := httptest.NewRecorder()
	mux.ServeHTTP(getResponse, get)
	var snapshot api.ActiveInputSnapshot
	if err := json.NewDecoder(getResponse.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if getResponse.Code != http.StatusOK || snapshot.State != api.ActiveInputEmpty || len(snapshot.RecoveryWorkflowIDs) != 0 {
		t.Fatalf("foreign recovery discovery = %#v, status=%d", snapshot, getResponse.Code)
	}
	response := serveLegacyRecoveryRequest(t.Context(), t, mux, current, "/api/app/OpenActiveInput",
		`{"request":{"idempotencyKey":"open-1","goal":"input_ready","intent":{"preparation":{"SourcePath":"Example.mkv"}}}}`)
	var body struct {
		Failure api.OperationFailure `json:"failure"`
	}
	responseBody := response.Body.String()
	if err := json.Unmarshal([]byte(responseBody), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || body.Failure.Code != api.OperationFailureUnknownOutcome ||
		body.Failure.Recovery != api.OperationRecoveryConfirm || !strings.Contains(body.Failure.Message, "Recover the interrupted input") {
		t.Fatalf("open response status=%d failure=%#v", response.Code, body.Failure)
	}
	select {
	case entry := <-entries:
		if !strings.Contains(entry.Message, "reserve input") || !strings.Contains(entry.Message, "[REDACTED]") {
			t.Fatalf("missing sanitized admission diagnostic: %q", entry.Message)
		}
		for _, private := range []string{`D:\private`, "Example.mkv", "secret-value"} {
			if strings.Contains(entry.Message, private) || strings.Contains(responseBody, private) {
				t.Fatal("admission failure leaked private diagnostic data")
			}
		}
	default:
		t.Fatal("admission failure was not logged")
	}
}

func serveLegacyRecoveryRequest(
	ctx context.Context,
	t *testing.T,
	mux *http.ServeMux,
	current session,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewBufferString(body))
	request.Host = "127.0.0.1:8080"
	request.RemoteAddr = "127.0.0.1:5050"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("X-Csrf-Token", current.CSRFToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestActiveInputRoutesValidateBeforeDispatch(t *testing.T) {
	tests := []struct {
		name  string
		route string
		body  string
		valid bool
	}{
		{
			name:  "open missing preparation",
			route: "OpenActiveInput",
			body:  `{"request":{"idempotencyKey":"open-1","goal":"input_ready"}}`,
		},
		{
			name:  "open with workflow authority",
			route: "OpenActiveInput",
			body:  `{"request":{"authority":{"workflowId":"workflow-1","expectedRevision":1},"idempotencyKey":"open-1","goal":"input_ready","intent":{"preparation":{"SourcePath":"Example.mkv"}}}}`,
		},
		{
			name:  "open missing idempotency",
			route: "OpenActiveInput",
			body:  `{"request":{"goal":"input_ready","intent":{"preparation":{"SourcePath":"Example.mkv"}}}}`,
		},
		{
			name:  "open invalid goal",
			route: "OpenActiveInput",
			body:  `{"request":{"idempotencyKey":"open-1","goal":"invalid","intent":{"preparation":{"SourcePath":"Example.mkv"}}}}`,
		},
		{
			name:  "open missing source",
			route: "OpenActiveInput",
			body:  `{"request":{"idempotencyKey":"open-1","goal":"input_ready","intent":{"preparation":{}}}}`,
		},
		{
			name:  "recover missing workflow",
			route: "RecoverLegacyActiveInput",
			body:  `{}`,
		},
		{
			name:  "reconcile missing authority",
			route: "ReconcileActiveInput",
			body:  `{}`,
		},
		{
			name:  "reconcile mismatched revision",
			route: "ReconcileActiveInput",
			body:  `{"authority":{"workflowId":"workflow-1","expectedRevision":2},"answer":{"actionId":"action-1","workflowRevision":1},"idempotencyKey":"resolve-1"}`,
		},
		{
			name:  "open valid",
			route: "OpenActiveInput",
			body:  `{"request":{"idempotencyKey":"open-1","goal":"input_ready","intent":{"preparation":{"SourcePath":"Example.mkv"}}}}`,
			valid: true,
		},
		{
			name:  "release empty slot",
			route: "ReleaseActiveInput",
			body:  `{}`,
			valid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state.db"))
			capability := &activeInputRouteFake{}
			server.backend.replaceRuntime(config.Config{}, CoreCapabilities{ReleaseWorkflow: capability}, nil)
			mux := http.NewServeMux()
			server.registerActiveInputRoutes(mux)
			current, err := server.sessions.Create("admin", false)
			if err != nil {
				t.Fatal(err)
			}
			response := serveLegacyRecoveryRequest(t.Context(), t, mux, current, "/api/app/"+test.route, test.body)
			wantStatus, wantCalls := http.StatusBadRequest, 0
			if test.valid {
				wantStatus, wantCalls = http.StatusOK, 1
			}
			if response.Code != wantStatus || capability.calls != wantCalls {
				t.Fatalf("status=%d calls=%d; want status=%d calls=%d", response.Code, capability.calls, wantStatus, wantCalls)
			}
		})
	}
}
