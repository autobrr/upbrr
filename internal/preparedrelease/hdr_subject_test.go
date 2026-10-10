// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"os"
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRCollectorRetiresRejectedHandoffs(t *testing.T) {
	for _, mode := range []string{"source mismatch", "invalid bluray choice", "hydrated source mismatch"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "hdr-provisional", "capture-owned")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			lease, err := preparationstate.NewHDRCaptureLease(directory)
			if err != nil {
				t.Fatal(err)
			}
			captures := map[string]preparationstate.HDRCaptureResource{"00001.MPLS": {Directory: directory, Lease: lease}}
			t.Cleanup(func() { preparationstate.ReleaseHDRCaptures(captures) })
			path := filepath.Join(root, "synthetic.mkv")
			collector, err := NewEvidenceCollector(privateResourcePipelineFake{state: preparationstate.State{
				SourcePath: path,
				Paths:      []string{path},
				Discs:      []preparationstate.DiscResource{{HDRCaptures: captures}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			request := preparationstate.Request{Manifest: api.SourceManifest{SourcePath: filepath.Join(root, "other.mkv")}}
			if mode == "invalid bluray choice" {
				request.Manifest.SourcePath = path
				request.Input.Instructions.BlurayReleaseID = "missing-candidate"
			}
			if mode == "hydrated source mismatch" {
				_, err = collector.HydratePrivateResources(t.Context(), request)
			} else {
				_, err = collector.Collect(t.Context(), request)
			}
			if err == nil {
				t.Fatal("invalid handoff was accepted")
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("rejected handoff retained its capture: %v", err)
			}
		})
	}
}

func TestHDRDiscClonePreservesOnlyLiveLeaseIdentity(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "hdr-provisional", "capture-owned")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	lease, err := preparationstate.NewHDRCaptureLease(directory)
	if err != nil {
		t.Fatal(err)
	}
	captures := map[string]preparationstate.HDRCaptureResource{"00001.MPLS": {
		Directory: directory,
		Lease:     lease,
		Failure:   &api.HDRAnalysisFailure{Message: "original"},
	}}
	t.Cleanup(func() { preparationstate.ReleaseHDRCaptures(captures) })
	original := []preparationstate.DiscResource{{
		HDRCaptures:       captures,
		FileList:          []string{"original"},
		SelectedPlaylists: []api.PlaylistInfo{{Items: []api.PlaylistItem{{File: "original"}}}},
		Reports:           []preparationstate.DiscReportResource{{Playlist: api.PlaylistInfo{Items: []api.PlaylistItem{{File: "original"}}}}},
	}}
	cloned := cloneDiscResources(original)
	if cloned[0].HDRCaptures["00001.MPLS"].Lease != lease {
		t.Fatal("clone discarded live owner identity")
	}
	cloned[0].FileList[0], cloned[0].SelectedPlaylists[0].Items[0].File, cloned[0].Reports[0].Playlist.Items[0].File = "changed", "changed", "changed"
	cloned[0].HDRCaptures["00001.MPLS"].Failure.Message = "changed"
	delete(cloned[0].HDRCaptures, "00001.MPLS")
	if original[0].FileList[0] != "original" || original[0].SelectedPlaylists[0].Items[0].File != "original" ||
		original[0].Reports[0].Playlist.Items[0].File != "original" || captures["00001.MPLS"].Failure.Message != "original" {
		t.Fatal("clone shared mutable resources with its owner")
	}
}

func TestHDRInventoryUsesSelectedPlaylistsAndMatchingCapture(t *testing.T) {
	root := t.TempDir()
	owned := envelope{resources: preparationResources{discs: []preparationstate.DiscResource{{
		Type:              "BDMV",
		Root:              root,
		VideoPath:         filepath.Join(root, "largest.m2ts"),
		SelectedPlaylists: []api.PlaylistInfo{{File: "1"}, {File: "2"}},
		HDRCaptures: map[string]preparationstate.HDRCaptureResource{
			"00001.MPLS": {Path: "matching.json", SourceFingerprint: "current"},
			"00002.MPLS": {Path: "stale.json", SourceFingerprint: "old"},
		},
	}}}}
	owned.result.Release.Compatibility.SourceFingerprint = "current"
	targets := hdrTargetSubjects(owned)
	if !targets[0].Target.Supported || targets[1].Target.Supported {
		t.Fatalf("capture eligibility=%#v", targets)
	}
	if len(targets) != 2 || targets[0].Target.Playlist != "00001.MPLS" || targets[1].Target.Playlist != "00002.MPLS" ||
		targets[0].CapturedPath != "matching.json" || targets[1].CapturedPath != "" || targets[0].Path != "" ||
		targets[0].Target.ID != api.HDRTargetID(filepath.Clean(root), "00001.MPLS") || targets[1].Target.Label != "Disc 1 · 00002.MPLS" {
		t.Fatalf("prepared disc inventory=%#v", targets)
	}
	failure := api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceLimit, Message: "capture exceeded its limit"}
	owned.resources.discs[0].HDRCaptures["00001.MPLS"] = preparationstate.HDRCaptureResource{SourceFingerprint: "current", Failure: &failure}
	targets = hdrTargetSubjects(owned)
	if targets[0].CapturedFailure == nil || targets[0].CapturedFailure.Code != failure.Code || targets[0].Target.Reason != failure.Message {
		t.Fatalf("capture failure was dropped: %#v", targets)
	}
	targets[0].CapturedFailure.Message = "changed"
	if failure.Message == "changed" {
		t.Fatal("target shared mutable capture failure")
	}
	owned.resources.discs[0].Type = "ISO"
	if targets := hdrTargetSubjects(owned); len(targets) != 0 {
		t.Fatalf("unsupported disc granted authority: %#v", targets)
	}
}

func TestHDRFileInventoryRequiresPerTargetMediaInfoConfirmation(t *testing.T) {
	first, second := filepath.Join(t.TempDir(), "Synthetic.HDR10+.mkv"), filepath.Join(t.TempDir(), "Synthetic.HDR10+.mkv")
	owned := envelope{resources: preparationResources{hdrFileEligibility: map[string]bool{canonicalSourceKey(first): true}}}
	owned.result.Release.Source.Entries = []api.SourceManifestEntry{{Type: api.SourceEntryTypeFile, Path: first}, {Type: api.SourceEntryTypeFile, Path: second}}
	targets := hdrTargetSubjects(owned)
	if len(targets) != 2 || !targets[0].Target.Supported || targets[1].Target.Supported || targets[1].Target.Reason == "" {
		t.Fatalf("per-file gate=%#v", targets)
	}
}

func TestHDRInventoryUsesOriginalMKVsAndStaleGenerationIsTyped(t *testing.T) {
	owned := envelope{}
	owned.result.Release.Source.Entries = []api.SourceManifestEntry{
		{Type: api.SourceEntryTypeFile, Path: "Synthetic.A.MKV"},
		{Type: api.SourceEntryTypeFile, Path: "Synthetic.B.mkv"},
		{Type: api.SourceEntryTypeFile, Path: "largest.m2ts"},
	}
	targets := hdrTargetSubjects(owned)
	if len(targets) != 2 || targets[0].Path != "Synthetic.A.MKV" || targets[1].Path != "Synthetic.B.mkv" || targets[0].Target.SelectionPolicy != "unique_hevc" {
		t.Fatalf("prepared file inventory=%#v", targets)
	}
	module := &Module{envelopes: make(map[string]envelope)}
	_, err := module.ResolveHDRAnalysisSubject(t.Context(), api.HDRAnalysisInstructions{
		Release: api.ReleaseRef{SourcePath: targets[0].Path, Generation: 2}, TargetIDs: []string{targets[0].Target.ID},
	})
	failure, ok := api.AsHDRAnalysisFailure(err)
	if !ok || failure.Code != api.HDRAnalysisFailureStaleSource {
		t.Fatalf("stale prepared generation=%#v err=%v", failure, err)
	}
}
