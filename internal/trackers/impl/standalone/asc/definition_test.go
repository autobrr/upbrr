// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func prepareDryRun(ctx context.Context, input trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	input.Intent = trackers.PreparationIntentDryRun
	plan, failure := New().Prepare(ctx, input)
	if failure != nil {
		return api.TrackerDryRunEntry{}, failure
	}
	return plan.DryRun(), nil
}

const testCookieFileName = "ASC.txt"

func TestDefinitionBuildUploadDryRunBlockedWithoutCookies(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "movie.mkv"),
			TorrentPath: torrentPath,
			Release: api.ReleaseInfo{
				Title:      "Movie",
				Year:       2024,
				Resolution: "1080p",
			},
			Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{
					Poster:   "https://img/poster.jpg",
					Overview: "Overview",
					Genres:   "Drama",
				},
				IMDB: &api.IMDBMetadata{IMDbIDText: "tt1234567"},
			},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")},
		}),
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
}

func TestDefinitionBuildUploadDryRunQuestionnaireForMissingMetadata(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "# Netscape HTTP Cookie File\n.cliente.amigos-share.club\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, testCookieFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	torrentPath := filepath.Join(tmp, "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "movie.mkv"),
			TorrentPath: torrentPath,
			Release: api.ReleaseInfo{
				Title:      "Movie",
				Year:       2024,
				Resolution: "1080p",
			},
			Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Poster: "https://img/poster.jpg"},
				IMDB: &api.IMDBMetadata{IMDbIDText: "tt1234567"},
			},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")},
		}),
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Questionnaire == nil {
		t.Fatal("expected questionnaire")
	}
	if got := len(entry.Questionnaire.Fields); got != 2 {
		t.Fatalf("expected 2 questionnaire fields, got %d", got)
	}
}
