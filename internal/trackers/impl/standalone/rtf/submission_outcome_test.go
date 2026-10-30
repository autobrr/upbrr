// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSubmissionOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		body    string
		outcome submission.Outcome
	}{
		{`{`, submission.Unknown},
		{`{}`, submission.Unknown},
		{`{"error":false,"torrent":{"id":0}}`, submission.Unknown},
		{`{"error":false,"torrent":{"id":{}}}`, submission.Unknown},
		{`{"error":true,"message":"Invalid torrent"}`, submission.Rejected},
		{`{"error":false,"torrent":{"id":123}}`, submission.Accepted},
		{`{"error":false,"torrent":{"id":1234567}}`, submission.Accepted},
	} {
		t.Run(test.body, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusCreated)
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
			if test.outcome == submission.Accepted && summary.UploadedTorrents[0].TorrentID != "123" && summary.UploadedTorrents[0].TorrentID != "1234567" {
				t.Fatalf("numeric identity=%q", summary.UploadedTorrents[0].TorrentID)
			}
		})
	}
}
