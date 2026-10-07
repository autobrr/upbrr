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
