// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/submission"
)

func TestSubmitASCUsesMigratedProtocol(t *testing.T) {
	t.Parallel()
	const registered = "d4:infod6:lengthi1e4:name9:fixture.x12:piece lengthi1e6:pieces20:01234567890123456789ee"
	for _, tc := range []struct {
		name, redirect string
		want           submission.Outcome
		failDownload   bool
		failScreenshot bool
	}{
		{
			name:     "registered",
			redirect: siteURL + "/torrents/42",
			want:     submission.Accepted,
		},
		{
			name:         "missing registered file",
			redirect:     "/torrents/42",
			want:         submission.Accepted,
			failDownload: true,
		},
		{
			name:     "foreign redirect",
			redirect: "https://foreign.example/torrents/42",
			want:     submission.Unknown,
		},
		{
			name:     "expired session",
			redirect: "/login",
			want:     submission.Rejected,
		},
		{
			name:           "screenshot error",
			want:           submission.NotSent,
			failScreenshot: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			client := &http.Client{Transport: ascTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != siteHost {
					t.Fatal("unexpected request outside ASC")
				}
				call := r.Method + " " + r.URL.Path
				calls = append(calls, call)
				w := httptest.NewRecorder()
				switch call {
				case "GET " + ascUploadFormPath:
					if _, err := r.Cookie("amigos-share-club-session"); err != nil {
						t.Fatal("upload form request missing session cookie")
					}
					http.SetCookie(w, &http.Cookie{
						Name:  "XSRF-TOKEN",
						Value: "synthetic%3D",
						Path:  "/",
					})
				case "POST " + ascScreenshotPath, "POST " + ascUploadPath:
					if r.Header.Get("X-Xsrf-Token") != "synthetic=" || r.Header.Get("Origin") != siteURL {
						t.Fatal("upload request missing Laravel headers")
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal("invalid multipart form")
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					if r.URL.Path == ascScreenshotPath {
						if len(r.MultipartForm.File["image"]) != 1 {
							t.Fatal("screenshot request missing image")
						}
						if tc.failScreenshot {
							w.WriteHeader(http.StatusTooManyRequests)
							return w.Result(), nil
						}
						_, _ = fmt.Fprint(w, `{"path":"screenshots/synthetic.webp"}`)
					} else {
						if len(r.MultipartForm.File["torrent"]) != 1 ||
							!slices.Equal(r.MultipartForm.Value["screenshot_paths[]"], []string{"screenshots/synthetic.webp"}) {
							t.Fatal("torrent submission missing file or stored screenshot path")
						}
						w.Header().Set("Location", tc.redirect)
						w.WriteHeader(http.StatusFound)
					}
				case "GET /torrents/42/download":
					if tc.failDownload {
						w.WriteHeader(http.StatusNotFound)
					} else {
						_, _ = fmt.Fprint(w, registered)
					}
				default:
					t.Fatal("unexpected ASC request")
				}
				return w.Result(), nil
			})}
			output := filepath.Join(t.TempDir(), "registered.torrent")
			result, err := submitASC(t.Context(), client, map[string]string{"amigos-share-club-session": "synthetic"}, nil,
				[]commonhttp.FileField{{FieldName: "torrent", Content: []byte("synthetic torrent")}},
				[]commonhttp.FileField{{FieldName: "image", Content: []byte("synthetic screenshot")}}, output)
			if outcome := submission.OutcomeOf(err); outcome != tc.want {
				t.Fatalf("outcome = %v, want %v: %v", outcome, tc.want, err)
			}
			wantCalls := []string{"GET " + ascUploadFormPath, "POST " + ascScreenshotPath}
			if !tc.failScreenshot {
				wantCalls = append(wantCalls, "POST "+ascUploadPath)
			}
			if tc.want == submission.Accepted {
				wantCalls = append(wantCalls, "GET /torrents/42/download")
				if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 || result.UploadedTorrents[0].TorrentID != "42" {
					t.Fatal("missing registered torrent identity")
				}
				if tc.failDownload {
					if result.UploadedTorrents[0].TorrentPath != "" {
						t.Fatal("should skip client injection without the registered file")
					}
				} else if payload, err := os.ReadFile(result.UploadedTorrents[0].TorrentPath); err != nil || !bytes.Equal(payload, []byte(registered)) {
					t.Fatal("registered torrent was not saved byte for byte")
				}
			}
			if !slices.Equal(calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, wantCalls)
			}
		})
	}
}
