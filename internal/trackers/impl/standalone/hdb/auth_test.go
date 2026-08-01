// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveAuthSessionUsesUploadForm(t *testing.T) {
	t.Parallel()

	requestedPath := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath <- r.URL.Path
		if r.URL.Path == "/upload" {
			_, _ = w.Write([]byte(`<form action="/upload/upload"></form>`))
		}
	}))
	defer server.Close()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	cookiePath, err := servicedb.CookiePath(dbPath, "HDB.json")
	if err != nil {
		t.Fatalf("CookiePath: %v", err)
	}
	if err := os.WriteFile(cookiePath, []byte(`{"session":"test"}`), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	err = resolveAuthSessionAt(
		context.Background(),
		config.TrackerConfig{Username: "user", Passkey: "test"},
		dbPath,
		api.TrackerAuthLoginRequest{},
		server.URL,
		server.Client(),
	)
	if err != nil {
		t.Fatalf("resolveAuthSessionAt: %v", err)
	}
	if got := <-requestedPath; got != "/upload" {
		t.Fatalf("request path = %q, want /upload", got)
	}
}
