// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWriteAppErrorBDMVRescanRequired(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeAppError(recorder, &api.BDMVRescanRequiredError{
		SourcePath:        `/disc`,
		SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		CachedPlaylists:   []string{"00001.MPLS"},
		MissingPlaylists:  []string{"00002.MPLS"},
	})

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", recorder.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload["code"]; got != api.ErrCodeBDMVRescanRequired {
		t.Fatalf("expected code %q, got %#v", api.ErrCodeBDMVRescanRequired, got)
	}
	if got := payload["source_path"]; got != "/disc" {
		t.Fatalf("unexpected source path %#v", got)
	}
}
