// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import "testing"

func TestCalculatePlaylistScoreCapsComponentsAndPenalizesRepeatedPlayItems(t *testing.T) {
	const (
		gb           = 1024 * 1024 * 1024
		longerThan4h = 5 * 60 * 60
	)

	items := []PlaylistItem{
		{File: "00001.m2ts", Size: 200 * gb},
		{File: "00001.m2ts", Size: 200 * gb},
		{File: "00001.m2ts", Size: 200 * gb},
		{File: "00001.m2ts", Size: 200 * gb},
	}

	got := calculatePlaylistScore(longerThan4h, items)
	const want = 92.5
	if got != want {
		t.Fatalf("calculatePlaylistScore() = %.2f, want %.2f", got, want)
	}
}

func TestCalculatePlaylistScoreRewardsUniquePlayItemConcentration(t *testing.T) {
	const gb = 1024 * 1024 * 1024

	repeated := []PlaylistItem{
		{File: "00001.m2ts", Size: 10 * gb},
		{File: "00001.m2ts", Size: 10 * gb},
		{File: "00001.m2ts", Size: 10 * gb},
		{File: "00001.m2ts", Size: 10 * gb},
	}
	unique := []PlaylistItem{
		{File: "00001.m2ts", Size: 10 * gb},
		{File: "00002.m2ts", Size: 10 * gb},
		{File: "00003.m2ts", Size: 10 * gb},
		{File: "00004.m2ts", Size: 10 * gb},
	}

	repeatedScore := calculatePlaylistScore(3600, repeated)
	uniqueScore := calculatePlaylistScore(3600, unique)
	if uniqueScore <= repeatedScore {
		t.Fatalf("expected unique playlist score %.2f to exceed repeated playlist score %.2f", uniqueScore, repeatedScore)
	}
}
