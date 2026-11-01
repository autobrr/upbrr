// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

type issue860Client struct {
	searches int
	result   api.ClientSearchResult
}

// CLI composite execution and WebUI dry runs both use this shared builder.
func TestWorkflowUploadPlanUsesRealTorrentReuseAndRecheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	other := filepath.Join(dir, "other", "video.mkv")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{source: "source-data", other: "source-evil"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mkbrr.Create(mkbrr.CreateOptions{Path: other, OutputPath: source + ".torrent"}); err != nil {
		t.Fatal(err)
	}
	retained := &workflowRetainedUploadServiceFake{}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{SourcePath: source}},
		trackers: retained,
		torrents: torrent.NewService(nil, t.TempDir()),
	}
	var verified, generated atomic.Int64
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 && update.Message == "Verifying torrent pieces" {
			verified.Add(1)
		}
		if update.TotalPieces > 0 && strings.Contains(update.Message, "Hashing") {
			generated.Add(1)
		}
	})
	build := func(instructions api.DescriptionInstructions) error {
		_, execution, err := builder.Build(ctx,
			api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
				TrackerID:   "EXAMPLE",
				Readiness:   api.ReadinessStatusReady,
				UploadReady: true,
			}}},
			api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
				TrackerID: "EXAMPLE",
				Decision:  api.DupeDecisionNoMatch,
				Status:    api.StageStatusCompleted,
			}}},
			workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, instructions,
			releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
		if execution != nil {
			if releaseErr := execution.Release(); err == nil {
				err = releaseErr
			}
		}
		return err
	}
	if err := build(api.DescriptionInstructions{Torrent: api.TorrentOverrides{NoHash: new(true)}}); err != nil || verified.Load() != 0 || generated.Load() != 0 {
		t.Fatalf("ordinary reuse: verified=%d generated=%d err=%v", verified.Load(), generated.Load(), err)
	}
	if err := build(api.DescriptionInstructions{Client: api.ClientOverrides{ForceRecheck: new(true)}, Torrent: api.TorrentOverrides{NoHash: new(true)}}); err == nil || verified.Load() == 0 {
		t.Fatalf("explicit recheck must reject mismatched media: verified=%d err=%v", verified.Load(), err)
	}
	verified.Store(0)
	if err := build(api.DescriptionInstructions{Torrent: api.TorrentOverrides{Rehash: new(true)}}); err != nil || generated.Load() == 0 {
		t.Fatalf("explicit rehash: generated=%d err=%v", generated.Load(), err)
	}
	generated.Store(0)
	if err := build(api.DescriptionInstructions{Torrent: api.TorrentOverrides{NoHash: new(true)}}); err != nil || verified.Load() != 0 || generated.Load() != 0 {
		t.Fatalf("repeated preparation: verified=%d generated=%d err=%v", verified.Load(), generated.Load(), err)
	}
}

func (c *issue860Client) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}
func (c *issue860Client) SearchPathedTorrents(_ context.Context, subject api.ClientSubject) (api.ClientSearchResult, error) {
	c.searches++
	if !subject.FindAllCandidates {
		panic("policy-driven search must request alternatives")
	}
	return c.result, nil
}

func TestWorkflowSearchesFurtherOnlyWhenPolicyRequiresIt(t *testing.T) {
	t.Parallel()
	source, candidates := issue860CandidateFixture(t)
	for _, test := range []struct {
		name         string
		first        int
		wantSearches int
	}{
		{name: "compatible existing artifact", first: 1},
		{
			name:         "hard policy requires alternative",
			first:        0,
			wantSearches: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clients := &issue860Client{result: api.ClientSearchResult{TorrentCandidates: []api.ClientTorrentCandidate{candidates[1]}}}
			builder := workflowUploadPlanBuilder{torrents: torrent.NewService(api.NopLogger{}, t.TempDir()), clients: clients}
			input := api.TorrentSubject{
				SourcePath:            source,
				SourceSize:            11,
				Trackers:              []string{"PTP"},
				ClientTorrentPath:     candidates[test.first].Path,
				ClientTorrentInfoHash: candidates[test.first].InfoHash,
				TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
			}
			result, err := builder.prepareWorkflowTorrent(t.Context(), api.UploadSubject{ClientSearchPerformed: true}, input)
			if err != nil || result.InfoHash != candidates[1].InfoHash || clients.searches != test.wantSearches {
				t.Fatalf("result=%+v searches=%d err=%v", result, clients.searches, err)
			}
		})
	}
}

func issue860CandidateFixture(t *testing.T) (string, []api.ClientTorrentCandidate) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates := make([]api.ClientTorrentCandidate, 0, 2)
	for index, exp := range []uint{19, 18} {
		path := filepath.Join(dir, []string{"large.torrent", "compatible.torrent"}[index])
		if _, err := mkbrr.Create(mkbrr.CreateOptions{
			Path:           source,
			OutputPath:     path,
			PieceLengthExp: &exp,
			MaxPieceLength: &exp,
		}); err != nil {
			t.Fatal(err)
		}
		mi, err := metainfo.LoadFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, api.ClientTorrentCandidate{Path: path, InfoHash: mi.HashInfoBytes().String()})
	}
	return source, candidates
}

func TestWorkflowUploadPlanContinuesWithPolicySelectedArtifact(t *testing.T) {
	t.Parallel()
	source, candidates := issue860CandidateFixture(t)
	clients := &issue860Client{result: api.ClientSearchResult{TorrentCandidates: []api.ClientTorrentCandidate{candidates[1]}}}
	retained := &workflowRetainedUploadServiceFake{torrentPaths: map[api.TrackerID]string{"PTP": candidates[1].Path}}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:            source,
			SourceSize:            11,
			InfoHash:              candidates[0].InfoHash,
			ClientTorrentPath:     candidates[0].Path,
			ClientSearchPerformed: true,
		}},
		trackers: retained,
		torrents: torrent.NewService(nil, t.TempDir()),
		clients:  clients,
	}
	var pieceWork atomic.Int64
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			pieceWork.Add(1)
		}
	})
	_, execution, err := builder.Build(ctx,
		api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
			TrackerID:   "PTP",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		}}},
		api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
			TrackerID: "PTP",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		}}},
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{},
		api.DescriptionInstructions{Torrent: api.TorrentOverrides{NoHash: new(true)}},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = execution.Release() }()
	if retained.subject.TorrentPath != candidates[1].Path || clients.searches != 1 || pieceWork.Load() != 0 {
		t.Fatalf("wrong selected artifact or unnecessary work: subject=%+v searches=%d pieces=%d", retained.subject, clients.searches, pieceWork.Load())
	}
	_, err = execution.ResolveAction(ctx, "PTP", api.RequiredActionResolveTrackerPreparation, true)
	if err != nil || clients.searches != 1 || pieceWork.Load() != 0 {
		t.Fatalf("continuation repeated client search or media work: searches=%d pieces=%d err=%v", clients.searches, pieceWork.Load(), err)
	}
}

func TestWorkflowSearchesExactFileBeforeConvertingWrapper(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	folder := filepath.Join(dir, "wrapped")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, filepath.Join(folder, "video.mkv")} {
		if err := os.WriteFile(path, []byte("source-data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := filepath.Join(dir, "wrapped.torrent")
	exact := filepath.Join(dir, "exact.torrent")
	for _, item := range []struct{ source, path string }{{folder, wrapper}, {source, exact}} {
		if _, err := mkbrr.Create(mkbrr.CreateOptions{Path: item.source, OutputPath: item.path}); err != nil {
			t.Fatal(err)
		}
	}
	wrappedMeta, err := metainfo.LoadFromFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	exactMeta, err := metainfo.LoadFromFile(exact)
	if err != nil {
		t.Fatal(err)
	}
	clients := &issue860Client{result: api.ClientSearchResult{TorrentCandidates: []api.ClientTorrentCandidate{{Path: exact, InfoHash: exactMeta.HashInfoBytes().String()}}}}
	builder := workflowUploadPlanBuilder{torrents: torrent.NewService(nil, t.TempDir()), clients: clients}
	input := api.TorrentSubject{
		SourcePath:              source,
		ClientTorrentCandidates: []api.ClientTorrentCandidate{{Path: wrapper, InfoHash: wrappedMeta.HashInfoBytes().String()}},
		TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
	}
	result, err := builder.prepareWorkflowTorrent(t.Context(), api.UploadSubject{ClientSearchPerformed: true}, input)
	if err != nil || result.InfoHash != exactMeta.HashInfoBytes().String() || clients.searches != 1 {
		t.Fatalf("exact alternative should precede wrapper conversion: result=%+v searches=%d err=%v", result, clients.searches, err)
	}
}
