// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildUploadDryRunIncludesQuestionnaire(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "ANT",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			ExternalIDs:       api.ExternalIDs{TMDBID: 123},
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Genres: "Adult", Keywords: "adult"},
			},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		AppConfig:     config.Config{},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Questionnaire == nil {
		t.Fatal("expected questionnaire")
	}
	if got := len(entry.Questionnaire.Fields); got != 3 {
		t.Fatalf("expected 3 questionnaire fields, got %d", got)
	}
	if entry.Questionnaire.Fields[0].Key != "type" {
		t.Fatalf("expected type field first, got %q", entry.Questionnaire.Fields[0].Key)
	}
	if entry.Questionnaire.Fields[0].Kind != "select" {
		t.Fatalf("expected type field to use select control, got %q", entry.Questionnaire.Fields[0].Kind)
	}
	if got := len(entry.Questionnaire.Fields[0].Options); got != 4 {
		t.Fatalf("expected 4 type options, got %d", got)
	}
	if entry.Questionnaire.Fields[2].Kind != "select" {
		t.Fatalf("expected adult screens field to use select control, got %q", entry.Questionnaire.Fields[2].Kind)
	}
}

func TestDefinitionBuildUploadDryRunMarksManualTagsWhenOnlyIMDbGenresExist(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker: "ANT",
		Meta: api.PreparedMetadata{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			ExternalIDs:       api.ExternalIDs{TMDBID: 123},
			ExternalMetadata: api.ExternalMetadata{
				IMDB: &api.IMDBMetadata{Genres: "Action, Drama"},
			},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		AppConfig:     config.Config{},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := entry.Payload["flagchangereason"]; got != "User prompted to add tags manually" {
		t.Fatalf("expected manual tag prompt reason, got %q", got)
	}
	if _, ok := entry.Payload["tags"]; ok {
		t.Fatalf("expected imdb fallback genres to avoid automatic ANT tags, got %#v", entry.Payload)
	}
}

func TestBuildDescriptionRemovesScreenshotOnlyBlockAndDefaultSignature(t *testing.T) {
	description, err := buildDescription(trackers.UploadRequest{}, trackers.DescriptionAssets{
		Description: `[align=center]
[url=https://ptpimg.me/fv71hr.png][img width=350]https://ptpimg.me/fv71hr.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(description) != "" {
		t.Fatalf("expected screenshot-only/signature-only description removed, got %q", description)
	}
}
