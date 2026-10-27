// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// regression: #803
func TestANTTrumpChallengeRetainsFinalOutcome(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	const key = "synthetic-key-12"
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	challenge := fmt.Sprintf(
		`{"error":"Slot occupied","dupe_of":7,"dupe_url":"https://example.invalid/torrents/7","slot":"WEB/1080","trump_key":%q,"expires":%q}`,
		key, expires,
	)
	for _, tc := range []struct {
		name, first, second                                  string
		intended, networkFailure, emptyReason                bool
		firstStatus, secondStatus, wantRequests, wantUploaded int
	}{
		{
			name: "challenge succeeds",
			first: challenge,
			second: `{"status":"success","view":"https://anthelion.me/torrents.php?torrentid=8"}`,
			intended: true,
			firstStatus: 409,
			secondStatus: 200,
			wantRequests: 2,
			wantUploaded: 1,
		},
		{
			name: "challenge succeeds without optional reason",
			first: challenge,
			second: `{"status":"success","view":"https://anthelion.me/torrents.php?torrentid=8"}`,
			intended: true,
			emptyReason: true,
			firstStatus: 409,
			secondStatus: 200,
			wantRequests: 2,
			wantUploaded: 1,
		},
		{
			name: "staff token accepted immediately",
			first: `{"status":"success"}`,
			firstStatus: 200,
			wantRequests: 1,
			wantUploaded: 1,
		},
		{
			name: "staff token with Guru intent",
			first: `{"status":"success"}`,
			intended: true,
			firstStatus: 200,
			wantRequests: 1,
			wantUploaded: 1,
		},
		{
			name: "no intended class flow",
			first: challenge,
			firstStatus: 409,
			wantRequests: 1,
		},
		{
			name: "ordinary conflict",
			first: `{"error":"Duplicate"}`,
			intended: true,
			firstStatus: 409,
			wantRequests: 1,
		},
		{
			name: "wrong status",
			first: challenge,
			intended: true,
			firstStatus: 403,
			wantRequests: 1,
		},
		{
			name: "invalid key",
			first: strings.Replace(challenge, key, "short", 1),
			intended: true,
			firstStatus: 409,
			wantRequests: 1,
		},
		{
			name: "expired key",
			first: strings.Replace(challenge, expires, "2000-01-01T00:00:00Z", 1),
			intended: true,
			firstStatus: 409,
			wantRequests: 1,
		},
		{
			name: "rejected initial key",
			first: challenge[:len(challenge)-1] + `,"key_error":true}`,
			intended: true,
			firstStatus: 409,
			wantRequests: 1,
		},
		{
			name: "replacement key is not retried",
			first: challenge,
			second: challenge[:len(challenge)-1] + `,"key_error":"expired"}`,
			intended: true,
			firstStatus: 409,
			secondStatus: 409,
			wantRequests: 2,
		},
		{
			name: "retry rejected",
			first: challenge,
			second: `{"error":"Rejected key synthetic-key-12"}`,
			intended: true,
			firstStatus: 409,
			secondStatus: 403,
			wantRequests: 2,
		},
		{
			name: "uncertain retry",
			first: challenge,
			intended: true,
			networkFailure: true,
			firstStatus: 409,
			wantRequests: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "example.torrent")
			torrent := []byte("captured torrent bytes")
			if err := os.WriteFile(path, torrent, 0o600); err != nil {
				t.Fatal(err)
			}
			body, contentType, err := buildMultipartPayload(map[string]string{"api_key": "test-key", "action": "upload"}, path)
			if err != nil {
				t.Fatal(err)
			}
			reason := "Verified improvement"
			if tc.emptyReason {
				reason = ""
			}
			requests := 0
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if err := req.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				defer req.MultipartForm.RemoveAll()
				file, _, err := req.FormFile("file_input")
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(file)
				_ = file.Close()
				if err != nil || !bytes.Equal(got, torrent) {
					t.Fatalf("torrent changed: %q, %v", got, err)
				}
				if requests == 1 {
					if req.FormValue("trump_key") != "" || req.FormValue("trump_reason") != "" {
						t.Fatal("initial upload contains challenge fields")
					}
					// The retry must consume captured bytes, not reread this file.
					if err := os.WriteFile(path, []byte("changed after preparation"), 0o600); err != nil {
						t.Fatal(err)
					}
					return &http.Response{
						StatusCode: tc.firstStatus,
						Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(tc.first)),
					}, nil
				}
				if requests != 2 || req.FormValue("trump_key") != key || req.FormValue("trump_reason") != reason || req.FormValue("api_key") != "test-key" {
					t.Fatal("invalid retry payload or retry count")
				}
				if tc.emptyReason {
					if _, present := req.MultipartForm.Value["trump_reason"]; present {
						t.Fatal("empty optional reason must be omitted")
					}
				}
				if tc.networkFailure {
					return nil, errors.New("connection interrupted with key " + key)
				}
				return &http.Response{
					StatusCode: tc.secondStatus,
					Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(tc.second)),
				}, nil
			})
			summary, err := submitPreparedUpload(t.Context(), api.NopLogger{}, uploadState{trumpRetry: tc.intended, trumpReason: reason}, body, contentType, "", "")
			if requests != tc.wantRequests || summary.Uploaded != tc.wantUploaded || (err == nil) != (tc.wantUploaded == 1) {
				t.Fatalf("requests=%d summary=%+v err=%v", requests, summary, err)
			}
			if err != nil && strings.Contains(err.Error(), key) {
				t.Fatal("challenge key exposed")
			}
			if tc.wantUploaded == 1 && tc.wantRequests == 2 {
				if len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentID != "8" ||
					summary.UploadedTorrents[0].TorrentURL != "https://anthelion.me/torrents.php?torrentid=8" {
					t.Fatal("receipt must use the final retry response")
				}
			}
			if tc.wantUploaded == 1 && len(summary.UploadedTorrents) != 1 {
				t.Fatal("final registration authority lost")
			}
		})
	}
}

// regression: #803
func TestTrumpRetryErrorRedactsWithoutLosingCause(t *testing.T) {
	const key = "synthetic-key-12"
	cause := fmt.Errorf("response read failed with %s: %w", key, io.ErrUnexpectedEOF)
	err := trumpRetryError{cause: cause, key: key}
	if strings.Contains(err.Error(), key) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("retry error must redact the key and preserve its failure cause")
	}
}
