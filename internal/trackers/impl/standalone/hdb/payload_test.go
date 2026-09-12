// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMultipartTorrentFilename(t *testing.T) {
	torrentPath := filepath.Join(t.TempDir(), "prepared.torrent")
	content := []byte("prepared torrent bytes")
	if err := os.WriteFile(torrentPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{
			name: "Example Film 2026 WEB-DL DD+5.1 HDR10+ DTS:X-GRP",
			want: "Example.Film.2026.WEB-DL.DDP5.1.HDR10P.DTS-X-GRP.torrent",
		},
		{
			name: "Café & Someone’s 'Story' 2026-GRP",
			want: "Cafe.and.Someones.Story.2026-GRP.torrent",
		},
		{
			name: "../Example\\Film: [2026]\"\r\n\t日本語😀 / 1080p_GRP...",
			want: "Example.Film.2026.1080p_GRP.torrent",
		},
		{
			name: "Example.Film.2026.1080p.WEB-DL-GRP",
			want: "Example.Film.2026.1080p.WEB-DL-GRP.torrent",
		},
		{
			name: "日本語😀..._-",
			want: "release.torrent",
		},
	} {
		t.Run(tc.want, func(t *testing.T) {
			body, contentType, err := buildMultipartPayload(map[string]string{"name": tc.name}, torrentPath)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), "POST", "/upload", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := req.MultipartForm.RemoveAll(); err != nil {
					t.Error(err)
				}
			})
			if got := req.FormValue("name"); got != tc.name {
				t.Fatalf("display name = %q, want %q", got, tc.name)
			}
			file, header, err := req.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if header.Filename != tc.want {
				t.Fatalf("filename = %q, want %q", header.Filename, tc.want)
			}
			got, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, content) {
				t.Fatal("torrent content changed")
			}
		})
	}
}
