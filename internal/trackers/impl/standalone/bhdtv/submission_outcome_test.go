// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

type outcomeTransport func(*http.Request) (*http.Response, error)

func (f outcomeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUploadSubmissionOutcomes(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, test := range []struct {
		body    string
		outcome submission.Outcome
	}{
		{`{}`, submission.Unknown},
		{`{"success":false,"message":"Duplicate torrent","data":{"view":"https://tracker.invalid/details/123"}}`, submission.Rejected},
		{`{"success":true,"data":{"view":"https://tracker.invalid/details/123"}}`, submission.Accepted},
	} {
		t.Run(test.body, func(t *testing.T) {
			requests := 0
			http.DefaultTransport = outcomeTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.body)),
					Request:    r,
				}, nil
			})
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, uploadState{}, nil, "application/octet-stream")
			if got := submission.OutcomeOf(err); got != test.outcome || requests != 1 || (summary.Uploaded == 1) != (test.outcome == submission.Accepted) {
				t.Fatalf("outcome=%v uploaded=%d requests=%d error=%v", got, summary.Uploaded, requests, err)
			}
		})
	}
}
