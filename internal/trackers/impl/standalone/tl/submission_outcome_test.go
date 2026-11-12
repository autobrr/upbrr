// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadRequiresAcceptedIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		status    int
		location  string
		body      string
		api       bool
		accepted  bool
		truncated bool
	}{
		{"login redirect", 302, "/login", "", false, false, false},
		{"valid redirect", 302, "/successfulupload?torrentID=123", "", false, true, false},
		{"accepted redirect with failed body", 302, "/successfulupload?torrentID=123", "partial", false, true, true},
		{"web redirect is not API receipt", 302, "/successfulupload?torrentID=123", "", true, false, false},
		{"zero API id", 200, "", "0", true, false, false},
		{"error status with numeric body", 400, "", "123", true, false, false},
		{"valid API id", 200, "", "123", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if test.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.Header().Set("Location", test.location)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{
				Logger: api.NopLogger{}, TrackerConfig: config.TrackerConfig{APIUpload: test.api},
			}, uploadState{endpoint: server.URL}, client, nil, "application/octet-stream", nil, "")
			if (err == nil) != test.accepted || (summary.Uploaded == 1) != test.accepted || requests.Load() != 1 {
				t.Fatalf("uploaded=%d requests=%d error=%v", summary.Uploaded, requests.Load(), err)
			}
		})
	}
}

func TestSuccessfulUploadReturnsBothRegisteredAnnounces(t *testing.T) {
	t.Parallel()
	for _, apiUpload := range []bool{false, true} {
		cfg := config.TrackerConfig{APIUpload: apiUpload, Passkey: "synthetic"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if apiUpload {
				_, _ = w.Write([]byte("123"))
				return
			}
			w.Header().Set("Location", "/successfulupload?torrentID=123")
			w.WriteHeader(http.StatusFound)
		}))
		t.Cleanup(server.Close)
		client := server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		info, err := bencode.Marshal(metainfo.Info{
			Name:        "Example.Release.2026",
			Length:      1024,
			PieceLength: 16384,
			Pieces:      make([]byte, 20),
			Source:      sourceFlag,
		})
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(t.TempDir(), "prepared.torrent")
		output := filepath.Join(t.TempDir(), "registered.torrent")
		prepared := metainfo.MetaInfo{InfoBytes: info}
		file, err := os.OpenFile(source, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepared.Write(file); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{TrackerConfig: cfg}, uploadState{endpoint: server.URL, torrentPath: source}, client, nil, "application/octet-stream", announceList(cfg), output)
		if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
			t.Fatalf("summary=%+v err=%v", summary, err)
		}
		registered, err := metainfo.LoadFromFile(summary.UploadedTorrents[0].TorrentPath)
		if err != nil {
			t.Fatal(err)
		}
		if registered.Announce != announceList(cfg)[0] || len(registered.AnnounceList) != 1 || !slices.Equal(registered.AnnounceList[0], announceList(cfg)) || registered.HashInfoBytes() != prepared.HashInfoBytes() || !strings.HasSuffix(registered.Comment, "\n"+torrentURL+"123") {
			t.Fatal("returned registered artifact lost announce or identity authority")
		}
	}
}
