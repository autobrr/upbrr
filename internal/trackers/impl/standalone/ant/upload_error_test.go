// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTUploadPreservesAPIErrorReason(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name, body, wantReason string
		status                 int
	}{
		{
			name:       "duplicate infohash",
			body:       `{"error":"A file with the same infohash already exists on the site!"}`,
			wantReason: "A file with the same infohash already exists on the site!",
			status:     http.StatusBadRequest,
		},
		{
			name:       "duplicate infohash retains view link",
			body:       `{"error":"A file with the same infohash already exists on the site!","view":"https://anthelion.me/torrents.php?torrentid=7"}`,
			wantReason: "A file with the same infohash already exists on the site! (https://anthelion.me/torrents.php?torrentid=7)",
			status:     http.StatusBadRequest,
		},
		{
			name:       "duplicate media",
			body:       `{"error":"The exact same media file is already present. Choose a different file."}`,
			wantReason: "The exact same media file is already present. Choose a different file.",
			status:     http.StatusBadRequest,
		},
		{
			name:       "forbidden detail",
			body:       `{"error":"Your upload permission has been revoked."}`,
			wantReason: "Your upload permission has been revoked.",
			status:     http.StatusForbidden,
		},
		{
			name:       "API rejection with HTTP success",
			body:       `{"success":false,"error":"The selected category is not permitted."}`,
			wantReason: "The selected category is not permitted.",
			status:     http.StatusOK,
		},
		{
			name:       "non-JSON API reason with HTTP success",
			body:       `Reason: {"error":"The selected category is not permitted."}`,
			wantReason: "The selected category is not permitted.",
			status:     http.StatusOK,
		},
		{
			name:       "secret redaction",
			body:       `{"error":"Invalid API key: api_key=never-print-this"}`,
			wantReason: "Invalid API key: api_key=[REDACTED]",
			status:     http.StatusForbidden,
		},
		{
			name:       "empty response fallback",
			wantReason: "ANT wrong API key or insufficient permissions",
			status:     http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				}, nil
			})
			summary, err := submitPreparedUpload(t.Context(), api.NopLogger{}, uploadState{}, nil, "application/json", "", "")
			if err == nil || !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("upload error = %v, want reason %q", err, tc.wantReason)
			}
			if strings.Contains(err.Error(), "never-print-this") {
				t.Fatal("upload error exposed an API key")
			}
			if summary.Uploaded != 0 || len(summary.UploadedTorrents) != 0 {
				t.Fatalf("rejected upload returned success: %+v", summary)
			}
		})
	}
}
