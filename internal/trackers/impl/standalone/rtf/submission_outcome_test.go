// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSubmissionOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		status    int
		body      string
		outcome   submission.Outcome
		torrentID string
	}{
		{"malformed", http.StatusCreated, `{`, submission.Unknown, ""},
		{"missing identity", http.StatusCreated, `{}`, submission.Unknown, ""},
		{"zero identity", http.StatusCreated, `{"error":false,"torrent":{"id":0}}`, submission.Unknown, ""},
		{"object identity", http.StatusCreated, `{"error":false,"torrent":{"id":{}}}`, submission.Unknown, ""},
		{"rejected with success status", http.StatusCreated, `{"error":true,"message":"Invalid torrent"}`, submission.Rejected, ""},
		{"complete rejection", http.StatusBadRequest, `{"error":true,"message":"Invalid torrent"}`, submission.Rejected, ""},
		{"truncated rejection prefix", http.StatusBadRequest, `{"error":true}` + strings.Repeat(" ", 1<<20) + `incomplete trailing data`, submission.Unknown, ""},
		{"numeric identity", http.StatusCreated, `{"error":false,"torrent":{"id":123}}`, submission.Accepted, "123"},
		{"large numeric identity", http.StatusCreated, `{"error":false,"torrent":{"id":1234567}}`, submission.Accepted, "1234567"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, uploadState{},
				server.URL, nil, "", "", "", "", "")
			if got := submission.OutcomeOf(err); got != test.outcome || requests.Load() != 1 {
				t.Fatalf("outcome=%v requests=%d error=%v", got, requests.Load(), err)
			}
			if (summary.Uploaded == 1) != (test.outcome == submission.Accepted) {
				t.Fatalf("uploaded=%d", summary.Uploaded)
			}
			if test.outcome == submission.Accepted && summary.UploadedTorrents[0].TorrentID != test.torrentID {
				t.Fatalf("numeric identity=%q", summary.UploadedTorrents[0].TorrentID)
			}
		})
	}
}
