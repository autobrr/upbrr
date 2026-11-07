// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

type outcomeTransport func(*http.Request) (*http.Response, error)

func (f outcomeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUploadRequiresAcceptanceEvidence(t *testing.T) {
	info, err := bencode.Marshal(metainfo.Info{
		Name:        "Example.Release.2026",
		Length:      1,
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: info}).Write(&payload); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "prepared.torrent")
	if err := os.WriteFile(source, payload.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, test := range []struct {
		body string
		page string
	}{
		{`{"error":"Invalid torrent"}`, ""},
		{`{"data":null}`, ""},
		{`{"data":"/"}`, ""},
		{`{"data":"https://tracker.invalid/torrents/123"}`, "https://tracker.invalid/torrents/123"},
		{`{"data":"/torrents/123"}`, "https://tvchaosuk.com/torrents/123"},
	} {
		t.Run(test.body, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "registered.torrent")
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
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}}, uploadState{torrentPath: source}, nil,
				"application/octet-stream", "", "https://tracker.invalid/announce", output)
			if (err == nil) != (test.page != "") || (summary.Uploaded == 1) != (test.page != "") || requests != 1 {
				t.Fatalf("uploaded=%d requests=%d error=%v", summary.Uploaded, requests, err)
			}
			if test.page != "" {
				retained, err := metainfo.LoadFromFile(output)
				if err != nil {
					t.Fatal(err)
				}
				if summary.UploadedTorrents[0].TorrentURL != test.page || retained.Comment != "uploaded with upbrr\n"+test.page {
					t.Fatal("upload lost its destination page in the retained torrent")
				}
			}
		})
	}
}
