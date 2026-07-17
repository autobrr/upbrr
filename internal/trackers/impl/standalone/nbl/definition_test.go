// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildUploadDryRunBuildsPayload(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Show.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	plan, failure := New().Prepare(context.Background(), trackers.PreparationInput{
		Tracker: "NBL",
		Intent:  trackers.PreparationIntentDryRun,
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Show.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{TVmazeID: 987},
			TVPack:            true,
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:        api.NopLogger{},
	})
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	entry := plan.DryRun()
	if entry.Payload["tvmazeid"] != "987" {
		t.Fatalf("expected tvmazeid 987, got %q", entry.Payload["tvmazeid"])
	}
	if entry.Payload["category"] != "3" {
		t.Fatalf("expected pack category 3, got %q", entry.Payload["category"])
	}
	if entry.Questionnaire != nil {
		t.Fatal("expected no questionnaire")
	}
}
