// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSeasonEpisodeHelpers(t *testing.T) {
	t.Parallel()

	state := State{
		SeasonInt: 9,
		Release: api.ReleaseInfo{
			Season:  1,
			Episode: 2,
		},
	}

	canonicalSeason, canonicalEpisode := state.CanonicalSeasonEpisode()
	if canonicalSeason != 9 || canonicalEpisode != 0 {
		t.Fatalf("canonical season/episode = %d/%d, want 9/0", canonicalSeason, canonicalEpisode)
	}

	fallbackSeason, fallbackEpisode := state.SeasonEpisodeWithParsedFallback()
	if fallbackSeason != 9 || fallbackEpisode != 2 {
		t.Fatalf("fallback season/episode = %d/%d, want 9/2", fallbackSeason, fallbackEpisode)
	}
	if !state.HasTVSeasonEpisodeSignal() {
		t.Fatal("expected parsed release episode to provide TV signal")
	}
}

func TestCloneClientEvidenceSnapshotDetachesNestedValues(t *testing.T) {
	t.Parallel()

	client := "qbit"
	original := ClientEvidenceSnapshot{
		Disposition: ClientEvidenceDispositionSearched,
		Policy:      api.ClientSearchPolicy{Client: &client},
		Result: api.ClientSearchResult{
			TrackerIDs:      map[string]string{"ant": "release-id"},
			MatchedTrackers: []string{"ANT"},
			TorrentComments: []api.TorrentMatch{{
				TrackerURLsRaw: []string{"https://tracker.example.invalid/announce"},
				TrackerURLs:    []api.TrackerMatch{{ID: "ANT"}},
			}},
		},
	}
	cloned := CloneClientEvidenceSnapshot(original)
	*cloned.Policy.Client = "other"
	cloned.Result.TrackerIDs["ant"] = "changed"
	cloned.Result.MatchedTrackers[0] = "CHANGED"
	cloned.Result.TorrentComments[0].TrackerURLsRaw[0] = "changed"
	cloned.Result.TorrentComments[0].TrackerURLs[0].ID = "CHANGED"

	if *original.Policy.Client != "qbit" || original.Result.TrackerIDs["ant"] != "release-id" ||
		original.Result.MatchedTrackers[0] != "ANT" ||
		original.Result.TorrentComments[0].TrackerURLsRaw[0] != "https://tracker.example.invalid/announce" ||
		original.Result.TorrentComments[0].TrackerURLs[0].ID != "ANT" {
		t.Fatalf("original snapshot was aliased: %#v", original)
	}
}
