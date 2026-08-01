// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestApplySkipAutoTorrentDefault(t *testing.T) {
	t.Parallel()

	if !applySkipAutoTorrentDefault(api.PrepareInput{}, true).Search.Skip {
		t.Fatal("configured skip_auto_torrent was not applied")
	}
	if !applySkipAutoTorrentDefault(api.PrepareInput{Search: api.ClientSearchPolicy{Skip: true}}, false).Search.Skip {
		t.Fatal("request skip_auto_torrent was not preserved")
	}
}

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
