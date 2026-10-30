// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadRequiresAcceptedIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		status    int
		location  string
		body      string
		api       bool
		accepted  bool
		truncated bool
	}{
		{"login redirect", 302, "/login", "", false, false, false},
		{"valid redirect", 302, "/successfulupload?torrentID=123", "", false, true, false},
		{"accepted redirect with failed body", 302, "/successfulupload?torrentID=123", "partial", false, true, true},
		{"web redirect is not API receipt", 302, "/successfulupload?torrentID=123", "", true, false, false},
		{"zero API id", 200, "", "0", true, false, false},
		{"error status with numeric body", 400, "", "123", true, false, false},
		{"valid API id", 200, "", "123", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if test.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.Header().Set("Location", test.location)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{
				Logger: api.NopLogger{}, TrackerConfig: config.TrackerConfig{APIUpload: test.api},
			}, uploadState{endpoint: server.URL}, client, nil, "application/octet-stream", nil, "")
			if (err == nil) != test.accepted || (summary.Uploaded == 1) != test.accepted || requests.Load() != 1 {
				t.Fatalf("uploaded=%d requests=%d error=%v", summary.Uploaded, requests.Load(), err)
			}
		})
	}
}
