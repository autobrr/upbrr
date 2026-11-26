// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/iotest"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/submission"
)

func TestSubmitASCUsesMigratedProtocol(t *testing.T) {
	t.Parallel()
	const root = "Synthetic Release"
	member := metainfo.FileInfo{Path: []string{"video.mkv"}, Length: 1}
	nested := metainfo.FileInfo{Path: []string{"Subs", "video.srt"}, Length: 1}
	submitted := ascProtocolTorrent(t, root, "", member, nested)
	// ASC may legitimately rewrite tracker-specific info fields.
	registered := ascProtocolTorrent(t, root, "ASC", member, nested)
	for _, tc := range []struct {
		name, redirect string
		want           submission.Outcome
		finalStatus    int
		incomplete     bool
		failDownload   bool
		failScreenshot bool
		mismatched     []byte
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
			name:       "different registered root",
			redirect:   "/torrents/42",
			want:       submission.Accepted,
			mismatched: ascProtocolTorrent(t, "Another Release", "ASC", member, nested),
		},
		{
			name:       "different registered form",
			redirect:   "/torrents/42",
			want:       submission.Accepted,
			mismatched: ascProtocolTorrent(t, root, "ASC"),
		},
		{
			name:       "different registered member path",
			redirect:   "/torrents/42",
			want:       submission.Accepted,
			mismatched: ascProtocolTorrent(t, root, "ASC", member, metainfo.FileInfo{Path: []string{"video.srt"}, Length: 1}),
		},
		{
			name:       "different registered member length",
			redirect:   "/torrents/42",
			want:       submission.Accepted,
			mismatched: ascProtocolTorrent(t, root, "ASC", member, metainfo.FileInfo{Path: nested.Path, Length: 2}),
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
name: "authentication rejected",
 finalStatus: http.StatusUnauthorized,
 want: submission.Rejected,
},
		{
name: "CSRF rejected",
 finalStatus: 419,
 want: submission.Rejected,
},
		{
name: "incomplete response",
 finalStatus: 419,
 incomplete: true,
 want: submission.Unknown,
},
		{
name: "ambiguous server error",
 finalStatus: http.StatusBadGateway,
 want: submission.Unknown,
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
						if tc.finalStatus != 0 {
							w.WriteHeader(tc.finalStatus)
							_, _ = fmt.Fprint(w, `{"message":"Synthetic middleware response"}`)
							if tc.incomplete {
								resp := w.Result()
								resp.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
								return resp, nil
							}
						} else {
							w.Header().Set("Location", tc.redirect)
							w.WriteHeader(http.StatusFound)
						}
					}
				case "GET /torrents/42/download":
					if tc.failDownload {
						w.WriteHeader(http.StatusNotFound)
					} else if tc.mismatched != nil {
						_, _ = w.Write(tc.mismatched)
					} else {
						_, _ = w.Write(registered)
					}
				default:
					t.Fatal("unexpected ASC request")
				}
				return w.Result(), nil
			})}
			output := filepath.Join(t.TempDir(), "registered.torrent")
			result, err := submitASC(t.Context(), client, map[string]string{"amigos-share-club-session": "synthetic"}, nil,
				[]commonhttp.FileField{{FieldName: "torrent", Content: submitted}},
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
				if tc.failDownload || tc.mismatched != nil {
					if result.UploadedTorrents[0].TorrentPath != "" {
						t.Fatal("should skip client injection without the registered file")
					}
					if _, err := os.Stat(output); !os.IsNotExist(err) {
						t.Fatalf("unexpected registered file: %v", err)
					}
				} else if payload, err := os.ReadFile(result.UploadedTorrents[0].TorrentPath); err != nil || !bytes.Equal(payload, registered) {
					t.Fatal("registered torrent was not saved byte for byte")
				}
			}
			if !slices.Equal(calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, wantCalls)
			}
		})
	}
}

func ascProtocolTorrent(t *testing.T, root, source string, files ...metainfo.FileInfo) []byte {
	t.Helper()
	info := metainfo.Info{
Name: root,
 Source: source,
 PieceLength: 4,
 Pieces: make([]byte, metainfo.HashSize),
 Files: files,
}
	if len(files) == 0 {
		info.Length = 2
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: encoded}).Write(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}
