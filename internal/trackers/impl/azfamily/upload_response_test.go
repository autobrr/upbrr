// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubmitPreparedUploadPreservesPartialHTMLFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "200")
		_, _ = io.WriteString(w, `<div class="error">Invalid category</div>`)
	}))
	defer server.Close()
	summary, err := submitPreparedUpload(
		t.Context(), siteDefinition{Name: "AZ", BaseURL: server.URL}, server.Client(), server.URL, nil, "", api.NopLogger{},
	)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "status=200") || !strings.Contains(err.Error(), "Invalid category") {
		t.Fatalf("partial response lost status, detail or read cause: %v", err)
	}
	if summary.Uploaded != 0 || len(summary.UploadedTorrents) != 0 {
		t.Fatalf("partial response returned registration authority: %+v", summary)
	}
}

func TestSubmitPreparedUploadPreservesLateHTMLFailure(t *testing.T) {
	t.Parallel()

	const detail = "AZ final submission rejected this release"
	server := lateHTMLFailureServer(t, http.StatusOK, detail)
	_, err := submitPreparedUpload(
		t.Context(),
		siteDefinition{Name: "AZ", BaseURL: server.URL},
		server.Client(),
		server.URL,
		nil,
		"",
		api.NopLogger{},
	)
	if err == nil || !strings.Contains(err.Error(), detail) {
		t.Fatalf("expected late HTML error detail, got %v", err)
	}
}

func TestCreateTaskPreservesLateNonSuccessHTMLFailure(t *testing.T) {
	t.Parallel()

	const detail = "AZ task rejected after form validation"
	server := lateHTMLFailureServer(t, http.StatusUnprocessableEntity, detail)
	torrentPath := filepath.Join(t.TempDir(), "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	_, err := createTask(
		t.Context(),
		siteDefinition{Name: "AZ", BaseURL: server.URL},
		sessionState{client: server.Client(), token: "token"},
		trackers.PreparationInput{Meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}},
		"1",
		"media info",
		torrentPath,
	)
	if err == nil || !strings.Contains(err.Error(), detail) {
		t.Fatalf("expected late task HTML error detail, got %v", err)
	}
}

func lateHTMLFailureServer(t *testing.T, status int, detail string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<html><body>" + strings.Repeat("padding ", 10*1024) + `<div class="alert-danger">` + detail + "</div></body></html>"))
	}))
	t.Cleanup(server.Close)
	return server
}
