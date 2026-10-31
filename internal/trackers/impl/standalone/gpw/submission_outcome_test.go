// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadRetainsAcceptanceWithoutIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","response":{}}`))
	}))
	defer server.Close()
	summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, uploadState{}, nil,
		"application/octet-stream", server.URL, "", "")
	if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentID != "" {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
}
