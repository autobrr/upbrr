// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSubmissionOutcomes(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, test := range []struct {
		body    string
		outcome submission.Outcome
	}{
		{`{}`, submission.Unknown},
		{`{"status":false,"error":true,"message":"Invalid torrent"}`, submission.Rejected},
		{`{"status":true,"error":false}`, submission.Accepted},
	} {
		t.Run(test.body, func(t *testing.T) {
			requests := 0
			http.DefaultTransport = questionnaireRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.body)),
					Request:    r,
				}, nil
			})
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, nil, "", "")
			if got := submission.OutcomeOf(err); got != test.outcome || requests != 1 || (summary.Uploaded == 1) != (test.outcome == submission.Accepted) {
				t.Fatalf("outcome=%v uploaded=%d requests=%d error=%v", got, summary.Uploaded, requests, err)
			}
			if test.outcome == submission.Accepted && summary.UploadedTorrents[0].TorrentID != "" {
				t.Fatal("missing remote identity became a fabricated torrent id")
			}
		})
	}
}
