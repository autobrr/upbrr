// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadRejectionRequiresProtocolEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		status  int
		body    string
		outcome submission.Outcome
	}{
		{"validation", 400, `{"error":"Invalid torrent"}`, submission.Rejected},
		{"duplicate existing torrent", 409, `{"id":123,"name":"Example.Release.2026"}`, submission.Rejected},
		{"bare conflict", 409, `{}`, submission.Unknown},
		{"server failure", 500, `{"error":"Internal error"}`, submission.Unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{
				Logger:  api.NopLogger{},
				Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(t.TempDir(), "app.sqlite")},
				Meta:    api.UploadSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")},
			}, uploadState{endpoint: server.URL, baseURL: server.URL}, nil, "application/octet-stream")
			if got := submission.OutcomeOf(err); got != test.outcome || requests.Load() != 1 || summary.Uploaded != 0 {
				t.Fatalf("outcome=%v requests=%d uploaded=%d error=%v", got, requests.Load(), summary.Uploaded, err)
			}
		})
	}
}
