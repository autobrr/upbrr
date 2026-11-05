// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/autobrr/go-torrent/metainfo"
)

func TestPersistRegisteredTorrentPreservesExactBytes(t *testing.T) {
	t.Parallel()

	payload := registeredTorrentTestPayload(t, "https://tracker.example/announce")
	outputPath := filepath.Join(t.TempDir(), "nested", "registered.torrent")
	if err := PersistRegisteredTorrent(outputPath, payload); err != nil {
		t.Fatalf("persist registered torrent: %v", err)
	}
	persisted, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read registered torrent: %v", err)
	}
	if !bytes.Equal(persisted, payload) {
		t.Fatal("persisted registered torrent bytes changed")
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat registered torrent: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("registered torrent permissions = %o, want user-only", info.Mode().Perm())
	}
}

func TestReconstructedRegisteredTorrentUsesDestinationPageComment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, url, comment string }{
		{
			name:    "destination page without private fields",
			url:     "https://user:secret@tracker.invalid/details.php?id=42&token=private#fragment",
			comment: "uploaded with upbrr\nhttps://tracker.invalid/details.php?id=42",
		},
		{
name: "unusable relative URL",
 url: "/details/42",
 comment: "uploaded with upbrr",
},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "prepared.torrent")
			output := filepath.Join(t.TempDir(), "registered.torrent")
			prepared := metainfo.MetaInfo{
				Announce:  "https://tracker.invalid/announce",
				Comment:   "https://source.invalid/details/123",
				InfoBytes: testInfoBytes(t, "REGISTERED"),
			}
			writeTestMetaInfo(t, source, prepared)
			if PersistReconstructedRegisteredTorrent(nil, "EXAMPLE", source, output, prepared.Announce, "REGISTERED", test.url) != output {
				t.Fatal("registered artifact was not retained")
			}
			retained := readTestMetaInfo(t, output)
			if retained.Comment != test.comment || retained.HashInfoBytes() != prepared.HashInfoBytes() || retained.Announce != prepared.Announce {
				t.Fatal("destination comment or registered identity was not preserved")
			}
			if readTestMetaInfo(t, source).Comment != prepared.Comment {
				t.Fatal("reconstruction modified the source torrent")
			}
		})
	}
}

func TestPersistRegisteredTorrentRejectsInvalidPayloadWithoutReplacingDestination(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "registered.torrent")
	original := registeredTorrentTestPayload(t, "https://tracker.example/original")
	if err := os.WriteFile(outputPath, original, 0o600); err != nil {
		t.Fatalf("write original registered torrent: %v", err)
	}
	if err := PersistRegisteredTorrent(outputPath, []byte("<html>not a torrent</html>")); err == nil {
		t.Fatal("expected invalid registered torrent rejection")
	}
	persisted, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read original registered torrent: %v", err)
	}
	if !bytes.Equal(persisted, original) {
		t.Fatal("invalid payload replaced original registered torrent")
	}
}

func TestDownloadRegisteredTorrentEnforcesStatusAndSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   []byte
	}{
		{
			name:   "non-success",
			status: http.StatusUnauthorized,
			body:   []byte("denied"),
		},
		{name: "empty", status: http.StatusOK},
		{
			name:   "invalid",
			status: http.StatusOK,
			body:   []byte("<html>invalid</html>"),
		},
		{
			name:   "oversized",
			status: http.StatusOK,
			body:   bytes.Repeat([]byte{'x'}, RegisteredTorrentMaxBytes+1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
			}))
			t.Cleanup(server.Close)
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("build registered torrent request: %v", err)
			}
			outputPath := filepath.Join(t.TempDir(), "registered.torrent")
			if err := DownloadRegisteredTorrent(context.Background(), server.Client(), request, outputPath); err == nil {
				t.Fatal("expected registered torrent download rejection")
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Fatal("rejected response left a registered torrent artifact")
			}
		})
	}
}

func TestDownloadRegisteredTorrentPersistsValidResponse(t *testing.T) {
	t.Parallel()

	payload := registeredTorrentTestPayload(t, "https://tracker.example/registered")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("build registered torrent request: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "registered.torrent")
	if err := DownloadRegisteredTorrent(context.Background(), server.Client(), request, outputPath); err != nil {
		t.Fatalf("download registered torrent: %v", err)
	}
	persisted, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read downloaded registered torrent: %v", err)
	}
	if !bytes.Equal(persisted, payload) {
		t.Fatal("downloaded registered torrent bytes changed")
	}
}

func registeredTorrentTestPayload(t *testing.T, announce string) []byte {
	t.Helper()

	var payload bytes.Buffer
	torrentMeta := metainfo.MetaInfo{
		Announce:  announce,
		InfoBytes: testInfoBytes(t, "REGISTERED"),
	}
	if err := torrentMeta.Write(&payload); err != nil {
		t.Fatalf("encode registered torrent: %v", err)
	}
	return payload.Bytes()
}
