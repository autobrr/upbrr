// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveTypeID(t *testing.T) {
	t.Parallel()

	if got := resolveTypeID(api.UploadSubject{
		Type:   "REMUX",
		Source: "Blu-ray",
	}); got != "14" {
		t.Fatalf("expected bluray remux type 14, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		Anime:   true,
		Release: api.ReleaseInfo{Resolution: "1080p"},
	}); got != "16" {
		t.Fatalf("expected anime hd type 16, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		TVPack:   true,
		Identity: api.ExternalIdentity{Category: "TV"},
		Release:  api.ReleaseInfo{Resolution: "480p"},
	}); got != "4" {
		t.Fatalf("expected tv pack sd type 4, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Release:  api.ReleaseInfo{Resolution: "1080p"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Genres: "Drama", Keywords: "adult"},
		},
	}); got != "13" {
		t.Fatalf("expected adult movie type 13, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunBlockedWithoutPoster(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "AR",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			Release:     api.ReleaseInfo{Title: "Movie", Resolution: "1080p"},
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
	if entry.Message != "missing poster URL" {
		t.Fatalf("expected poster block message, got %q", entry.Message)
	}
}
