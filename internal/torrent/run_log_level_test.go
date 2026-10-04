// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCreateUsesOperationLogLevel(t *testing.T) {
	t.Parallel()
	root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = root.Close() })
	scoped, err := logging.NewOperationLogger(root, "debug")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "example.torrent")
	createTestTorrent(t, filepath.Join(dir, "example.bin"), torrentPath)
	service := NewService(root, dir)
	if _, err := service.Create(logging.WithOperationLogger(t.Context(), scoped), api.TorrentSubject{SourcePath: torrentPath}); err != nil {
		t.Fatal(err)
	}
	entries := root.Recent(100)
	if len(entries) == 0 {
		t.Fatal("torrent diagnostics ignored the operation's debug level")
	}
	for _, entry := range entries {
		if entry.Level != "debug" {
			t.Fatalf("unexpected torrent log level %s", entry.Level)
		}
	}
	if _, err := service.Create(t.Context(), api.TorrentSubject{SourcePath: torrentPath}); err != nil {
		t.Fatal(err)
	}
	if len(root.Recent(100)) != len(entries) {
		t.Fatal("operation changed the shared torrent service logger")
	}
}
