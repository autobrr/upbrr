// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadRetainsOnlyKnownDetailPage(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"42", ""} {
		t.Run("ID="+id, func(t *testing.T) {
			t.Parallel()
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
			output := filepath.Join(t.TempDir(), "registered.torrent")
			if err := os.WriteFile(source, payload.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body := "Upload successful!"
				if id != "" {
					body += ` <a href="details.php?id=42">Details</a>`
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{Logger: api.NopLogger{}},
				uploadState{baseURL: server.URL, torrentPath: source}, nil, nil, "application/octet-stream", "https://tracker.invalid/announce", output)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
				t.Fatal("upload lost its successful outcome")
			}
			wantURL, wantComment := "", "uploaded with upbrr"
			if id != "" {
				wantURL = server.URL + "/details.php?id=" + id
				wantComment += "\n" + wantURL
			}
			retained, err := metainfo.LoadFromFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if summary.UploadedTorrents[0].TorrentURL != wantURL || retained.Comment != wantComment {
				t.Fatal("upload retained an incorrect destination detail link")
			}
		})
	}
}
