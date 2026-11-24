// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/autobrr/upbrr/internal/services/db"
)

func TestInputLogExportReportsLockedRetainedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.Mkdir(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	const retainedName = "upbrr.log.1"
	filePath := filepath.Join(logs, retainedName)
	if err := os.WriteFile(filePath, []byte("retained log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })

	root, err := os.OpenRoot(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Lstat(retainedName); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Lstat of locked log = %v, want sharing violation", err)
	}
	if _, err := ReadInputLog(t.Context(), nil, filepath.Join(dir, "upbrr.db"), db.InputLogAttempt{ID: "test"}); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("export of locked log = %v, want sharing violation", err)
	}
}
