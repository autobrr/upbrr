// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRCollectorConfirmationRequiresMatchingSuccessfulCapture(t *testing.T) {
	for _, test := range []struct {
		name      string
		capture   preparationstate.HDRCaptureResource
		confirmed bool
	}{
		{
			name: "confirmed",
			capture: preparationstate.HDRCaptureResource{
				Path:              "metadata.json",
				TrackID:           1,
				SourceFingerprint: "current",
			},
			confirmed: true,
		},
		{name: "old source", capture: preparationstate.HDRCaptureResource{
			Path:              "metadata.json",
			TrackID:           1,
			SourceFingerprint: "old",
		}},
		{name: "missing track", capture: preparationstate.HDRCaptureResource{Path: "metadata.json", SourceFingerprint: "current"}},
		{name: "missing metadata", capture: preparationstate.HDRCaptureResource{TrackID: 1, SourceFingerprint: "current"}},
		{name: "absent", capture: preparationstate.HDRCaptureResource{
			Path:              "metadata.json",
			TrackID:           1,
			SourceFingerprint: "current",
			Absent:            true,
		}},
		{name: "failed", capture: preparationstate.HDRCaptureResource{
			Path:              "metadata.json",
			TrackID:           1,
			SourceFingerprint: "current",
			Failure:           &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureInvalidBitstream},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			collector, err := NewEvidenceCollector(privateResourcePipelineFake{state: preparationstate.State{
				SourcePath: root,
				Paths:      []string{root},
				DiscType:   "BDMV",
				Discs: []preparationstate.DiscResource{{
					ID:                "disc",
					Name:              "Disc 1",
					Type:              "BDMV",
					Root:              root,
					SelectedPlaylists: []api.PlaylistInfo{{ID: "disc:00001.MPLS", File: "00001.MPLS"}},
					HDRCaptures:       map[string]preparationstate.HDRCaptureResource{"00001.MPLS": test.capture},
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			facts, err := collector.Collect(t.Context(), preparationstate.Request{
				Manifest: api.SourceManifest{SourcePath: root}, SourceFingerprint: "current",
			})
			if err != nil {
				t.Fatal(err)
			}
			if facts.Disc.Items[0].Reports[0].HDR10PlusConfirmed != test.confirmed {
				t.Fatalf("confirmation=%v, want %v", facts.Disc.Items[0].Reports[0].HDR10PlusConfirmed, test.confirmed)
			}
		})
	}
}

func TestHDRDiscConfirmationSurvivesSeedAndRestartWithoutProvisionalAuthority(t *testing.T) {
	root, pipeline, input := hdrConfirmedDiscFixture(t)
	collector, err := NewEvidenceCollector(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	module := newTestModule(t, store, collector)
	prepared, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: root, Generation: prepared.Release.Generation}
	initialTargets, err := module.HDRAnalysisTargets(t.Context(), ref)
	if err != nil || len(initialTargets) != 1 || !initialTargets[0].Supported {
		t.Fatalf("confirmed targets=%#v err=%v", initialTargets, err)
	}
	seed, err := module.Export(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	imported := newTestModule(t, newMemoryStore(), collector)
	importedRef, err := imported.Import(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := imported.ResolveHDRAnalysisSubject(t.Context(), api.HDRAnalysisInstructions{
		Release: importedRef, TargetIDs: []string{initialTargets[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if subject.Targets[0].CapturedPath != "" || subject.Targets[0].CapturedTrackID != 0 {
		t.Fatal("seed transferred provisional capture authority")
	}
	input.Controls.CaptureHDRMetadata = false
	restarted := newTestModule(t, store, collector)
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Release.Generation != prepared.Release.Generation || !reused.Release.Disc.Items[0].Reports[0].HDR10PlusConfirmed {
		t.Fatal("restart lost confirmed playlist facts")
	}
	targets, err := restarted.HDRAnalysisTargets(t.Context(), ref)
	if err != nil || len(targets) != 1 || !targets[0].Supported {
		t.Fatalf("restart targets=%#v err=%v", targets, err)
	}
	input.Force = true
	refreshed, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.Release.Disc.Items[0].Reports[0].HDR10PlusConfirmed {
		t.Fatal("ordinary refresh lost source-bound confirmation")
	}
}

func TestHDRDiscConfirmationClearsOnChangedSourceTimelineOrExplicitAbsence(t *testing.T) {
	for _, change := range []string{"source", "timeline", "explicit absence"} {
		t.Run(change, func(t *testing.T) {
			root, pipeline, input := hdrConfirmedDiscFixture(t)
			collector, err := NewEvidenceCollector(pipeline)
			if err != nil {
				t.Fatal(err)
			}
			module := newTestModule(t, newMemoryStore(), collector)
			if _, err := module.Prepare(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			input.Controls.CaptureHDRMetadata, input.Force = false, true
			switch change {
			case "source":
				if err := os.WriteFile(filepath.Join(root, "BDMV", "PLAYLIST", "00001.MPLS"), []byte("changed playlist bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "timeline":
				pipeline.file = "00002.MPLS"
			case "explicit absence":
				input.Controls.CaptureHDRMetadata, pipeline.absent = true, true
			}
			prepared, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Release.Disc.Items[0].Reports[0].HDR10PlusConfirmed {
				t.Fatal("stale confirmation survived changed eligibility")
			}
			targets, err := module.HDRAnalysisTargets(t.Context(), api.ReleaseRef{SourcePath: root, Generation: prepared.Release.Generation})
			if err != nil || len(targets) != 1 || targets[0].Supported {
				t.Fatalf("targets=%#v err=%v", targets, err)
			}
		})
	}
}

// The pipeline supplies native capture evidence; these tests exercise its canonical
// fact publication and persistence, without treating the fixture as a native-parser test.
type hdrConfirmedDiscPipeline struct {
	file   string
	absent bool
}

func (p *hdrConfirmedDiscPipeline) CollectPreparationEvidence(_ context.Context, request preparationstate.Request) (preparationstate.State, error) {
	disc := request.Layout.Discs[0]
	playlist := api.PlaylistInfo{
		ID:       disc.ID + ":" + p.file,
		DiscID:   disc.ID,
		DiscName: disc.Name,
		File:     p.file,
		Duration: 120,
	}
	resource := preparationstate.DiscResource{
		ID:                disc.ID,
		Name:              disc.Name,
		Root:              disc.Root,
		Type:              disc.Type,
		SelectedPlaylists: []api.PlaylistInfo{playlist},
	}
	if request.Input.Controls.CaptureHDRMetadata {
		resource.HDRCaptures = map[string]preparationstate.HDRCaptureResource{p.file: {
			Path:              filepath.Join(disc.Root, "metadata.json"),
			TrackID:           1,
			SourceFingerprint: request.SourceFingerprint,
			Absent:            p.absent,
		}}
	}
	return preparationstate.State{
		SourcePath: request.Manifest.SourcePath,
		Paths:      []string{request.Manifest.SourcePath},
		DiscType:   "BDMV",
		Discs:      []preparationstate.DiscResource{resource},
	}, nil
}

func (p *hdrConfirmedDiscPipeline) HydratePrivateResources(ctx context.Context, request preparationstate.Request) (preparationstate.State, error) {
	return p.CollectPreparationEvidence(ctx, request)
}

func hdrConfirmedDiscFixture(t *testing.T) (string, *hdrConfirmedDiscPipeline, api.PrepareInput) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "BDMV", "PLAYLIST"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"00001.MPLS", "00002.MPLS"} {
		if err := os.WriteFile(filepath.Join(root, "BDMV", "PLAYLIST", file), []byte("synthetic playlist"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, &hdrConfirmedDiscPipeline{file: "00001.MPLS"}, api.PrepareInput{
		SourcePath:   root,
		Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{Set: true, UseAll: true}},
		Controls:     api.PreparationControls{CaptureHDRMetadata: true},
	}
}
