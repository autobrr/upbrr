// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/apitoken"
)

func TestWebUIAPITokenManagementCreatesAuthenticatesListsAndRevokes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	writeTestAuthFile(t, dbPath, "operator", false)
	repository, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("open auth store: %v", err)
	}
	store, err := newAPITokenStore(nil, repository)
	if err != nil {
		t.Fatalf("new API token store: %v", err)
	}
	sessions, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("new sessions: %v", err)
	}
	t.Cleanup(sessions.Close)
	current, err := sessions.Create("operator", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	server := &Server{
		apiTokens:      store,
		sessions:       sessions,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	createResponse := performAuthenticatedAppRequest(
		t,
		mux,
		current,
		"/api/app/CreateAPIToken",
		`{"name":"Web automation","ownerId":"web-owner","scopes":["workflow:read"]}`,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created apitoken.Created
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Token == "" || created.Record.ID == "" {
		t.Fatal("created token or token ID is empty")
	}
	principal, ok, err := store.authenticateContext(ctx, created.Token)
	if err != nil || !ok || principal.OwnerID != "api:web-owner" {
		t.Fatalf("authenticate principal=%#v ok=%t err=%v", principal, ok, err)
	}

	listResponse := performAuthenticatedAppRequest(t, mux, current, "/api/app/ListAPITokens", `{}`)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), created.Token) {
		t.Fatal("list response exposed plaintext token")
	}
	var records []apitoken.Record
	if err := json.Unmarshal(listResponse.Body.Bytes(), &records); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(records) != 1 || records[0].ID != created.Record.ID {
		t.Fatalf("records = %#v", records)
	}

	revokeBody, err := json.Marshal(map[string]string{"id": created.Record.ID})
	if err != nil {
		t.Fatalf("encode revoke: %v", err)
	}
	revokeResponse := performAuthenticatedAppRequest(t, mux, current, "/api/app/RevokeAPIToken", string(revokeBody))
	if revokeResponse.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeResponse.Code, revokeResponse.Body.String())
	}
	if _, ok, err := store.authenticateContext(ctx, created.Token); err != nil || ok {
		t.Fatalf("revoked authenticate ok=%t err=%v", ok, err)
	}
}

func performAuthenticatedAppRequest(
	t *testing.T,
	handler http.Handler,
	current session,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost"+path, bytes.NewBufferString(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("X-Csrf-Token", current.CSRFToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
