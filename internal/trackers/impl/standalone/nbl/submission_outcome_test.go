// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type outcomeTransport func(*http.Request) (*http.Response, error)

func (f outcomeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUploadRequiresAcceptanceEvidence(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, test := range []struct {
		body     string
		accepted bool
	}{
		{"", false},
		{`{}`, false},
		{`{"status":"error","message":"Invalid torrent"}`, false},
		{`{"link":["https://tracker.invalid/torrents.php?id=123"]}`, false},
		{`{"link":"garbage id=123"}`, false},
		{`{"link":"https://tracker.invalid/torrents.php?id=123"}`, true},
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
			summary, err := submitPreparedUpload(t.Context(), api.NopLogger{}, uploadState{}, nil, "application/octet-stream", "", "")
			if (err == nil) != test.accepted || (summary.Uploaded == 1) != test.accepted || requests != 1 {
				t.Fatalf("uploaded=%d requests=%d error=%v", summary.Uploaded, requests, err)
			}
		})
	}
}
