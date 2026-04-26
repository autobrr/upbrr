// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveRuntimePrefersMediaInfoDuration(t *testing.T) {
	root := t.TempDir()
	miPath := filepath.Join(root, "MEDIAINFO.txt")
	if err := os.WriteFile(miPath, []byte("General\nDuration                                 : 1 h 31 min\n"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	meta := api.PreparedMetadata{
		MediaInfoTextPath: miPath,
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 91 {
		t.Fatalf("expected MediaInfo runtime 91 minutes, got %d", got)
	}
}

func TestResolveRuntimeFallsBackToExternalMetadata(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 95},
		},
	}

	if got := resolveRuntime(meta); got != 95 {
		t.Fatalf("expected IMDb runtime fallback, got %d", got)
	}
}

func TestResolveRuntimePrefersBDInfoLengthForBDMV(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		BDInfo: map[string]interface{}{
			"length": "01:40:00.000",
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{RuntimeMinutes: 120},
		},
	}

	if got := resolveRuntime(meta); got != 100 {
		t.Fatalf("expected BDInfo runtime 100 minutes, got %d", got)
	}
}
