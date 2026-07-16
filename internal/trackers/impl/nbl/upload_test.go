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

func TestPrepareUploadStateUsesNumericIgnoreDupes(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	mediaInfoPath := filepath.Join(tmp, "mediainfo.txt")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, []byte("mediainfo"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	state, err := prepareUploadState(context.Background(), trackers.PreparationInput{
		Meta: api.UploadSubject{
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
		},
		TrackerConfig: config.TrackerConfig{APIKey: "api-key"},
	})
	if err != nil {
		t.Fatalf("prepare upload state: %v", err)
	}
	if got := state.fields["ignoredupes"]; got != "1" {
		t.Fatalf("expected ignoredupes=1, got %q", got)
	}
}

func TestExtractUploadLinkAndIDFromRootLink(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"link": "https://nebulance.io/torrents.php?id=12345",
	}

	link, id := extractUploadLinkAndID(payload)
	if link != "https://nebulance.io/torrents.php?id=12345" {
		t.Fatalf("expected upload link from root, got %q", link)
	}
	if id != "12345" {
		t.Fatalf("expected torrent id 12345, got %q", id)
	}
}

func TestExtractUploadLinkAndIDFromResultLink(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"result": map[string]any{
			"link": "https://nebulance.io/torrents.php?id=67890",
		},
	}

	link, id := extractUploadLinkAndID(payload)
	if link != "https://nebulance.io/torrents.php?id=67890" {
		t.Fatalf("expected upload link from nested result, got %q", link)
	}
	if id != "67890" {
		t.Fatalf("expected torrent id 67890, got %q", id)
	}
}

func TestExtractUploadLinkAndIDEmptyWhenMissingLink(t *testing.T) {
	t.Parallel()

	payload := map[string]any{"status": "ok"}

	link, id := extractUploadLinkAndID(payload)
	if link != "" {
		t.Fatalf("expected empty upload link when missing, got %q", link)
	}
	if id != "" {
		t.Fatalf("expected empty torrent id when link missing, got %q", id)
	}
}
