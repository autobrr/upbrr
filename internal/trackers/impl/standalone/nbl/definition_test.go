// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileDeclaresNBLHDRDupeSlots(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "nbl/duplicate/v2" || policy.EvidenceID != "nbl-uploading-overview" ||
		!slices.Contains(policy.SlotDimensions, trackers.DupeDimensionHDR) ||
		policy.SearchScope.MaxPages != 100 ||
		policy.HDRSlotMode != trackers.DupeHDRSlotModeGeneric {
		t.Fatalf("NBL dupe policy = %#v", policy)
	}
}

func TestDefinitionBuildUploadDryRunBuildsPayload(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Show.torrent")
	screenshotPath := filepath.Join(tmp, "screenshot.png")
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
			ExactMedia: &api.ExactMediaAssets{
				Screenshots: []api.ScreenshotImage{{Path: screenshotPath, Purpose: api.ScreenshotPurposeFinal}},
				ScreenshotUploads: []api.UploadedImageLink{{
					ImagePath: screenshotPath,
					ImgURL:    "https://images.example.test/screenshot.png",
				}},
			},
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:        api.NopLogger{},
	})
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	entry := plan.DryRun()
	wantPayload := map[string]string{
		"action":      "upload",
		"api_key":     "token",
		"tvmazeid":    "987",
		"mediainfo":   "General\nUnique ID : 123",
		"category":    "3",
		"ignoredupes": "1",
	}
	if !maps.Equal(entry.Payload, wantPayload) {
		t.Fatalf("payload = %#v, want %#v", entry.Payload, wantPayload)
	}
	if len(entry.Files) != 1 || entry.Files[0].Field != "file_input" || entry.Files[0].Path != torrentPath {
		t.Fatalf("expected only the torrent attachment, got %+v", entry.Files)
	}
	if entry.Questionnaire != nil {
		t.Fatal("expected no questionnaire")
	}
}
