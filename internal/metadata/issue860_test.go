// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerMetadataUsesItsOwnClientInfoHash(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
		InfoHash:   "selected-artifact-hash",
		TrackerIDs: map[string]string{"ant": "42"},
		TorrentComments: []api.TorrentMatch{{
			Hash: "metadata-source-hash", TrackerURLs: []api.TrackerMatch{{ID: "ANT", TrackerID: "42"}},
		}},
	}
	record, _, _, err := NewService(&fakeRepo{}).lookupTrackerData(t.Context(), meta, "ANT", time.Now())
	if err != nil || record.InfoHash != "metadata-source-hash" || meta.InfoHash != "selected-artifact-hash" {
		t.Fatalf("tracker metadata lost its own hash: record=%+v artifact=%q err=%v", record, meta.InfoHash, err)
	}
}

func TestTrackerMetadataUsesHashFromRecognizedTrackerWithoutID(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath:      filepath.Join(t.TempDir(), "Example.mkv"),
		InfoHash:        "selected-artifact-hash",
		TorrentComments: []api.TorrentMatch{{Hash: "metadata-source-hash", MatchedTrackers: []string{"ANT"}}},
	}
	record, _, _, err := NewService(&fakeRepo{}).lookupTrackerData(t.Context(), meta, "ANT", time.Now())
	if err != nil || record.InfoHash != "metadata-source-hash" || meta.InfoHash != "selected-artifact-hash" {
		t.Fatalf("tracker URL evidence lost its hash: record=%+v artifact=%q err=%v", record, meta.InfoHash, err)
	}
}
