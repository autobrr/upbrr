// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// regression: #809
func TestRecoveryVaultPreservesProductionMedia(t *testing.T) {
	t.Parallel()
	repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	first, err := releaseworkflow.NewPrivateArtifactVault(releaseworkflow.PrivateArtifactVaultRoot(repo.DBPath()))
	if err != nil {
		t.Fatal(err)
	}
	original := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{
Path: filepath.Join(t.TempDir(), "capture.png"),
 Width: 19,
 Height: 11,
}},
	}
	now := time.Now().UTC()
	if err := first.Put("owner", "workflow", "media:media-1", original, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	recovery, err := NewRecoveryPrivateArtifactVault(repo)
	if err != nil {
		t.Fatal(err)
	}
	value, err := recovery.Get("owner", "workflow", "media:media-1", now)
	if err != nil {
		t.Fatalf("recovery lost retained media: %v", err)
	}
	media, ok := value.(workflowMediaPrivateArtifacts)
	if !ok || len(media.Screenshots) != 1 || media.Screenshots[0] != original.Screenshots[0] {
		t.Fatalf("recovery changed captured media: %#v", value)
	}
}
