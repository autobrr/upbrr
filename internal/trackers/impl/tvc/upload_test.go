// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareUploadStateSkipsTVDBForMovie(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	state, err := prepareUploadState(context.Background(), trackers.UploadRequest{
		Tracker: "TVC",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoCategory: "TV",
			ExternalIDs: api.ExternalIDs{
				Category: "MOVIE",
				TMDBID:   123,
				TVDBID:   456,
			},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Title: "Movie", Year: 2025},
			},
			Release: api.ReleaseInfo{Title: "Movie", Year: 2025, Resolution: "1080p"},
			Type:    "WEBDL",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	})
	if err != nil {
		t.Fatalf("prepare upload state: %v", err)
	}
	if _, ok := state.fields["tvdb"]; ok {
		t.Fatalf("did not expect tvdb for movie payload")
	}
}

func TestPrepareUploadStateIncludesTVDBForTV(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "show.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	state, err := prepareUploadState(context.Background(), trackers.UploadRequest{
		Tracker: "TVC",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Show.S02E03.mkv"),
			TorrentPath: torrentPath,
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
				TMDBID:   123,
				TVDBID:   456,
			},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Title: "Show", Year: 2025},
			},
			Release:    api.ReleaseInfo{Title: "Show", Year: 2025, Resolution: "1080p"},
			Type:       "WEBDL",
			SeasonInt:  2,
			EpisodeInt: 3,
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	})
	if err != nil {
		t.Fatalf("prepare upload state: %v", err)
	}
	if got := state.fields["tvdb"]; got != "456" {
		t.Fatalf("expected tvdb=456, got %q", got)
	}
}

func TestPrepareUploadStateIncludesTVDBForMediaInfoTV(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "show.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	state, err := prepareUploadState(context.Background(), trackers.UploadRequest{
		Tracker: "TVC",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Show.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoCategory: "TV",
			ExternalIDs:       api.ExternalIDs{TMDBID: 123, TVDBID: 456},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Title: "Show", Year: 2025},
			},
			Release: api.ReleaseInfo{Title: "Show", Year: 2025, Resolution: "1080p"},
			Type:    "WEBDL",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	})
	if err != nil {
		t.Fatalf("prepare upload state: %v", err)
	}
	if got := state.fields["tvdb"]; got != "456" {
		t.Fatalf("expected tvdb=456, got %q", got)
	}
}

func TestPrepareUploadStateIncludesTVDBForSeasonPackWithoutCategory(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "season.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	state, err := prepareUploadState(context.Background(), trackers.UploadRequest{
		Tracker: "TVC",
		Meta: api.PreparedMetadata{
			SourcePath:  filepath.Join(tmp, "Show.S02.mkv"),
			TorrentPath: torrentPath,
			ExternalIDs: api.ExternalIDs{
				TMDBID: 123,
				TVDBID: 456,
			},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Title: "Show", Year: 2025},
			},
			Release:   api.ReleaseInfo{Title: "Show", Year: 2025, Resolution: "1080p"},
			Type:      "WEBDL",
			TVPack:    true,
			SeasonInt: 2,
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	})
	if err != nil {
		t.Fatalf("prepare upload state: %v", err)
	}
	if got := state.fields["tvdb"]; got != "456" {
		t.Fatalf("expected tvdb=456, got %q", got)
	}
}
