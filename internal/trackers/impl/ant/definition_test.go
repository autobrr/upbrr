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
	"github.com/autobrr/upbrr/internal/paths"
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

func TestDefinitionBuildUploadDryRunUsesBDInfoForBDMV(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "db.sqlite")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	sourcePath := filepath.Join(tmp, "Movie", "BDMV")
	if err := os.MkdirAll(sourcePath, 0o700); err != nil {
		t.Fatalf("create source path: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	meta := api.PreparedMetadata{
		SourcePath:            sourcePath,
		TorrentPath:           torrentPath,
		DiscType:              "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{{File: "00001.MPLS"}},
		ExternalIDs:           api.ExternalIDs{TMDBID: 123},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{Genres: "Action"},
		},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"ANT": {"type": "Feature Film"},
		},
	}
	tmpDir, _, err := paths.ReleaseTempDir(filepath.Join(tmp, "tmp"), meta, sourcePath)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	bdinfoPath := paths.BDMVSummaryPath(tmpDir, "00001.MPLS")
	if err := os.WriteFile(bdinfoPath, []byte("BDINFO_CONTENT"), 0o600); err != nil {
		t.Fatalf("write bdinfo: %v", err)
	}

	entry, err := New().BuildUploadDryRun(context.Background(), trackers.UploadRequest{
		Tracker:       "ANT",
		Meta:          meta,
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		AppConfig:     config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := entry.Payload["bdinfo"]; got != "BDINFO_CONTENT" {
		t.Fatalf("expected BDInfo payload, got %q", got)
	}
	if got := entry.Payload["container_type"]; got != "m2ts" {
		t.Fatalf("expected m2ts container type, got %q", got)
	}
	if _, ok := entry.Payload["mediainfo"]; ok {
		t.Fatalf("expected BDMV upload to omit mediainfo, got %#v", entry.Payload)
	}
	if _, ok := entry.Payload["media"]; ok {
		t.Fatalf("expected BDMV upload to omit media field, got %#v", entry.Payload)
	}
}

func TestResolveFlagsIncludesIMAXAndCriterionEdition(t *testing.T) {
	t.Parallel()

	flags := resolveFlags(api.PreparedMetadata{Edition: "IMAX Criterion Collection"})
	if !containsString(flags, "IMAX") {
		t.Fatalf("expected IMAX flag, got %#v", flags)
	}
	if !containsString(flags, "Criterion") {
		t.Fatalf("expected Criterion flag from edition, got %#v", flags)
	}
}

func TestResolveReleaseGroupBansUpdatedGroups(t *testing.T) {
	t.Parallel()

	for _, group := range []string{"EVO", "SM737"} {
		if got, ok := resolveReleaseGroup(group); ok || got != "" {
			t.Fatalf("expected %s to be banned, got %q ok=%t", group, got, ok)
		}
	}
	if got, ok := resolveReleaseGroup("Flights"); !ok || got != "Flights" {
		t.Fatalf("expected Flights to be allowed, got %q ok=%t", got, ok)
	}
}

func TestBuildDescriptionRemovesScreenshotOnlyBlockAndDefaultSignature(t *testing.T) {
	description := buildDescription(trackers.UploadRequest{}, trackers.DescriptionAssets{
		Description: `[align=center]
[url=https://pixhost.to/fv71hr.png][img width=350]https://pixhost.to/fv71hr.png[/img][/url]
[/align]

[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
	})
	if strings.TrimSpace(description) != "" {
		t.Fatalf("expected screenshot-only/signature-only description removed, got %q", description)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
