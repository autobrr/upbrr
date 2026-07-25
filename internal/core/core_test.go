// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"testing"
)

func TestWorkflowPrivateVaultRootIsDatabaseScoped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstDB := filepath.Join(root, "first.db")
	secondDB := filepath.Join(root, "second.db")
	first := workflowPrivateVaultRoot(firstDB)
	if first != workflowPrivateVaultRoot(firstDB) {
		t.Fatal("same database path produced unstable private vault root")
	}
	if first == workflowPrivateVaultRoot(secondDB) {
		t.Fatal("distinct databases in one directory shared a private vault root")
	}
}
