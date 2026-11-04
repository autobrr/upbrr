// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadClassifiesResponseEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, id string
		status         int
		outcome        submission.Outcome
	}{
		{"success without identity", `{"status":"success","response":{}}`, "", http.StatusOK, submission.Accepted},
		{"ok without identity", `{"status":"ok","response":{}}`, "", http.StatusOK, submission.Accepted},
		{"success with error", `{"status":"success","error":"Invalid torrent","response":{}}`, "", http.StatusOK, submission.Unknown},
		{"ok with error and identity", `{"status":"ok","error":"Invalid torrent","response":{"torrent_id":"123"}}`, "", http.StatusOK, submission.Unknown},
		{"numeric status with error", `{"status":200,"error":"Invalid torrent","response":{"torrent_id":"123"}}`, "", http.StatusOK, submission.Unknown},
		{"numeric status with identity", `{"status":200,"response":{"torrent_id":"123"}}`, "123", http.StatusOK, submission.Accepted},
		{"numeric status without identity", `{"status":200,"response":{}}`, "", http.StatusOK, submission.Unknown},
		{"blank error", `{"status":"success","error":"  ","response":{}}`, "", http.StatusOK, submission.Accepted},
		{"non-2xx status with success body", `{"status":"success","response":{"torrent_id":"123"}}`, "", http.StatusBadRequest, submission.Unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, uploadState{}, nil,
				"application/octet-stream", server.URL, "", "")
			if requests.Load() != 1 || submission.OutcomeOf(err) != test.outcome {
				t.Fatalf("requests=%d outcome=%v error=%v", requests.Load(), submission.OutcomeOf(err), err)
			}
			if test.outcome == submission.Unknown {
				if err == nil || summary.Uploaded != 0 || len(summary.UploadedTorrents) != 0 {
					t.Fatalf("uncertain response produced authority: summary=%+v error=%v", summary, err)
				}
				return
			}
			wantURL := ""
			if test.id != "" {
				wantURL = torrentURL + test.id
			}
			if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 ||
				summary.UploadedTorrents[0].TorrentID != test.id || summary.UploadedTorrents[0].TorrentURL != wantURL {
				t.Fatalf("summary=%+v error=%v", summary, err)
			}
		})
	}
}
