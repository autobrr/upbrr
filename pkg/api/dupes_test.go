// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestNewAcceptedDuplicateEvidenceFiltersSelectionAndDeepCopiesResults(t *testing.T) {
	t.Parallel()

	summary := DupeCheckSummary{Results: []DupeCheckResult{
		{
			Tracker:   "BLU",
			Status:    "completed",
			Raw:       []DupeEntry{{Name: "Example.Release.2026.1080p-GRP", Files: []string{"video.mkv"}}},
			Match:     DupeMatch{MatchedEpisodeIDs: []DupeEpisodeMatch{{ID: "1"}}},
		},
		{Tracker: "AITHER", Status: "completed"},
	}}
	evidence := NewAcceptedDuplicateEvidence(
		ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 1},
		[]string{"blu"},
		summary,
	)
	if len(evidence.Results) != 1 || evidence.Results[0].Tracker != "BLU" {
		t.Fatalf("accepted results = %#v", evidence.Results)
	}
	evidence.Results[0].Raw[0].Files[0] = "mutated.mkv"
	evidence.Results[0].Match.MatchedEpisodeIDs[0].ID = "mutated"
	if summary.Results[0].Raw[0].Files[0] != "video.mkv" ||
		summary.Results[0].Match.MatchedEpisodeIDs[0].ID != "1" {
		t.Fatalf("summary mutated through evidence: %#v", summary.Results[0])
	}
}
