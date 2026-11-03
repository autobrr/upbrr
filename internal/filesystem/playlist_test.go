// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	bdrunner "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestISOPlaylistCandidatesPreserveVirtualTimeline(t *testing.T) {
	result := bdrunner.Result{
		Disc: bdrunner.DiscInfo{Is3D: true},
		Playlists: []bdrunner.PlaylistInfo{{
			Name:          "00800.MPLS",
			LengthSeconds: 120,
			IsValid:       true,
		}, {
			Name:          "00001.MPLS",
			LengthSeconds: 300,
			IsValid:       true,
		}},
		Timelines: []bdrunner.PlaylistTimeline{{Name: "00800.MPLS", Items: []bdrunner.PlayItem{
			{Angles: []bdrunner.Angle{{Source: video.Source{LogicalClip: "00001.M2TS"}}}},
			{Angles: []bdrunner.Angle{{Source: video.Source{LogicalClip: "00002.M2TS"}}}},
			{Angles: []bdrunner.Angle{{Source: video.Source{LogicalClip: "00001.M2TS"}}}},
		}}},
	}
	candidates, err := isoPlaylistCandidates(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[1].File != "00800.MPLS" || candidates[1].Duration != 120 || len(candidates[1].Items) != 3 {
		t.Fatalf("selected multi-clip candidate lost: %#v", candidates)
	}
	for _, item := range candidates[1].Items {
		if filepath.Base(item.File) != item.File || item.Size != 0 {
			t.Fatalf("invented physical stream: %#v", item)
		}
	}
	result.Disc.Is3D = false
	if _, err := isoPlaylistCandidates(result); err == nil {
		t.Fatal("2D ISO admitted")
	}
	result.Disc.Is3D = true
	result.Playlists = nil
	if _, err := isoPlaylistCandidates(result); err == nil {
		t.Fatal("empty ISO admitted")
	}
}

func TestDiscoverMalformedISORejectsWithoutVideoFallback(t *testing.T) {
	image := filepath.Join(t.TempDir(), "Example.3D.iso")
	if err := os.WriteFile(image, []byte("not a disc image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverPlaylists(t.Context(), image); err == nil || !strings.Contains(err.Error(), "decrypted 3D Blu-ray") {
		t.Fatalf("malformed ISO should give actionable rejection: %v", err)
	}
}
