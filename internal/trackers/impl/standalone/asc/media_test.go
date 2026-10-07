// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveMediaInfoReport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reportPath := filepath.Join(dir, "mediainfo.txt")
	if err := os.WriteFile(reportPath, []byte("General\r\nFormat : Matroska\r\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	dbPath := filepath.Join(dir, "ua.db")

	report, err := resolveMediaInfoReport(api.UploadSubject{MediaInfoTextPath: reportPath}, dbPath)
	if err != nil || report != "General\nFormat : Matroska" {
		t.Fatalf("report = %q err=%v", report, err)
	}

	missing := filepath.Join(dir, "missing.txt")
	if report, err := resolveMediaInfoReport(api.UploadSubject{MediaInfoTextPath: missing}, dbPath); err == nil || report != "" {
		t.Fatalf("unreadable report = %q err=%v, want error", report, err)
	}

	if report, err := resolveMediaInfoReport(api.UploadSubject{}, dbPath); err != nil || report != "" {
		t.Fatalf("no report = %q err=%v, want empty without error", report, err)
	}
}

func TestRenameMediaInfoFilesMatchesTorrentName(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Audio: "DD+ 5.1", Channels: "5.1"}
	report := "General\r\nUnique ID : 1\r\nComplete name                            : /data/Downloads/Example.Show.S01E05.1080p.WEB-DL.H.264-GRP/Example.Show.S01E05.1080p.WEB-DL.H.264-GRP.mkv\r\nFormat : Matroska\r\n"
	want := "General\r\nUnique ID : 1\r\nComplete name                            : /data/Downloads/Example.Show.S01E05.1080p.WEB-DL.H.264-GRP/Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv\r\nFormat : Matroska\r\n"
	if got := renameMediaInfoFiles(meta, report); got != want {
		t.Fatalf("renamed report = %q", got)
	}
	// Names that need no rename, and other lines, are untouched.
	plain := "Complete name : C:\\media\\Example.Movie.2020.mkv\nFormat : AVC\n"
	if got := renameMediaInfoFiles(meta, plain); got != plain {
		t.Fatalf("report without a rename changed: %q", got)
	}
}
