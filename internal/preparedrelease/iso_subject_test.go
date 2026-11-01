// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestISOScreenshotSummarySurvivesPreparedRestart(t *testing.T) {
	path := writePreparedTestFile(t, "Example.3D.iso", "synthetic image")
	layout, err := sourcelayout.Resolve(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	disc := layout.Discs[0]
	selected := api.PlaylistInfo{
		ID:       disc.ID + ":00800.MPLS",
		DiscID:   disc.ID,
		DiscName: disc.Name,
		File:     "00800.MPLS",
		Duration: 120,
	}
	summary := "Playlist: 00800.MPLS\nVideo: MPEG-4 MVC Video / 10000 kbps / 1080p / 23.976 fps\n"
	state := preparationstate.State{
		SourcePath:            path,
		Paths:                 []string{path},
		DiscType:              "BDMV",
		VideoPath:             path,
		SelectedBDMVPlaylists: []api.PlaylistInfo{selected},
		Discs: []preparationstate.DiscResource{{
			ID:                disc.ID,
			Name:              disc.Name,
			Type:              "BDMV",
			Root:              path,
			VideoPath:         path,
			BluRayISO:         true,
			SelectedPlaylists: []api.PlaylistInfo{selected},
			Reports: []preparationstate.DiscReportResource{
				{Playlist: api.PlaylistInfo{
					ID:       disc.ID + ":00001.MPLS",
					DiscID:   disc.ID,
					DiscName: disc.Name,
					File:     "00001.MPLS",
				}, Summary: "unselected report"},
				{Playlist: selected, Summary: summary},
			},
		}},
	}
	store := newMemoryStore()
	var generation api.PreparedGeneration
	for pass := range 2 {
		collector, err := NewEvidenceCollector(privateResourcePipelineFake{state: state})
		if err != nil {
			t.Fatal(err)
		}
		module := newTestModule(t, store, collector)
		result, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: path, Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"00800.MPLS"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if pass == 0 {
			generation = result.Release.Generation
		} else if generation != result.Release.Generation {
			t.Fatal("restart replaced compatible generation")
		}
		subject, err := module.ResolveScreenshotSubject(t.Context(), api.MediaPlanInput{Release: api.ReleaseRef{SourcePath: path, Generation: result.Release.Generation}})
		if err != nil {
			t.Fatal(err)
		}
		if len(subject.Discs) != 1 || !subject.Discs[0].BluRayISO || subject.Discs[0].BDInfoSummary != summary {
			t.Fatalf("selected prepared summary lost on pass %d: %+v", pass, subject.Discs)
		}
	}
}
