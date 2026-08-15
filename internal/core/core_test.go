// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestApplyMetadataDefaults(t *testing.T) {
	t.Parallel()

	configured := applyMetadataDefaults(api.PrepareInput{}, config.MetadataConfig{
		SkipAutoTorrent: true,
		KeepImages:      true,
		OnlyID:          true,
	})
	if !configured.Search.Skip || !configured.Policy.KeepImages || !configured.Policy.OnlyID {
		t.Fatalf("configured metadata defaults were not applied: %#v", configured)
	}

	requested := applyMetadataDefaults(api.PrepareInput{
		Search: api.ClientSearchPolicy{Skip: true},
		Policy: api.PreparationPolicy{KeepImages: true, OnlyID: true},
	}, config.MetadataConfig{})
	if !requested.Search.Skip || !requested.Policy.KeepImages || !requested.Policy.OnlyID {
		t.Fatalf("request metadata options were not preserved: %#v", requested)
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
