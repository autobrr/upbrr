// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestProviderDiagnosticsUseOperationLogLevel(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic provider unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = root.Close() })
	scoped, err := logging.NewOperationLogger(root, "debug")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.Client(), root, "synthetic-key")
	client.baseURL = server.URL
	result, err := client.FindByExternalID(logging.WithOperationLogger(t.Context(), scoped), FindInput{IMDbID: "tt1234567"})
	if err != nil || !result.FilenameSearch {
		t.Fatal("provider fallback changed")
	}
	entries := root.Recent(100)
	if len(entries) != 1 || entries[0].Level != "debug" {
		t.Fatal("provider diagnostic did not use operation verbosity")
	}
	if _, err := client.FindByExternalID(t.Context(), FindInput{IMDbID: "tt1234567"}); err != nil {
		t.Fatal(err)
	}
	if len(root.Recent(100)) != 1 {
		t.Fatal("provider diagnostic changed the shared client logger")
	}
}
