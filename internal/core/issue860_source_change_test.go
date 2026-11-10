// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowDiscardsGeneratedTorrentWhenSourceChanges(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	tmpRoot := t.TempDir()
	torrents := torrent.NewService(nil, tmpRoot)
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:     source,
			SourceManifest: inspected.Manifest,
			SourceIdentity: inspected.Identity,
		}},
		trackers: &workflowRetainedUploadServiceFake{},
		torrents: torrents,
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
	var mutateOnce sync.Once
	var mutationErr error
	ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		if update.Message != "Hashing complete" {
			return
		}
		mutateOnce.Do(func() {
			mutationErr = os.WriteFile(source, []byte("source-evil"), 0o600)
			if mutationErr == nil {
				changed := time.Now().Add(time.Hour)
				mutationErr = os.Chtimes(source, changed, changed)
			}
		})
	})
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
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
	if execution != nil {
		_ = execution.Release()
	}
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	if !errors.Is(err, preparedrelease.ErrSourceChanged) {
		t.Fatalf("expected changed source rejection, got %v", err)
	}
	path, err := torrent.TempTorrentPath(tmpRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed generation left reusable artifact: %s: %v", path, err)
	}
	if _, err := torrents.Create(t.Context(), api.TorrentSubject{SourcePath: source, TorrentOverrides: api.TorrentOverrides{NoHash: new(true)}}); err == nil {
		t.Fatal("retry reused a failed generation")
	}
}

func TestWorkflowPublishesGeneratedTorrentOnlyAfterStableSource(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	tmpRoot := t.TempDir()
	retained := &workflowRetainedUploadServiceFake{}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:     source,
			SourceManifest: inspected.Manifest,
			SourceIdentity: inspected.Identity,
		}},
		trackers: retained,
		torrents: torrent.NewService(nil, tmpRoot),
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
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
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = execution.Release() }()
	expected, err := torrent.TempTorrentPath(tmpRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(retained.subject.TorrentPath) != filepath.Join(filepath.Dir(expected), "base-torrents") {
		t.Fatalf("expected immutable base beneath release owner, got %s", retained.subject.TorrentPath)
	}
	if _, err := os.Stat(retained.subject.TorrentPath); err != nil {
		t.Fatalf("published artifact unavailable: %v", err)
	}
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(expected), ".generating-*.torrent"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("staged generation leaked: %v err=%v", staged, err)
	}
}

type issue860MutatingReuse struct {
	source, artifact string
}

func (s issue860MutatingReuse) Create(_ context.Context, _ api.TorrentSubject) (api.TorrentResult, error) {
	if err := os.WriteFile(s.source, []byte("source-evil"), 0o600); err != nil {
		return api.TorrentResult{}, fmt.Errorf("mutate test source: %w", err)
	}
	changed := time.Now().Add(time.Hour)
	if err := os.Chtimes(s.source, changed, changed); err != nil {
		return api.TorrentResult{}, fmt.Errorf("timestamp test source: %w", err)
	}
	return api.TorrentResult{Path: s.artifact}, nil
}

func TestWorkflowPreservesReusedTorrentWhenSourceChanges(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "existing.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{Path: source, OutputPath: artifact}); err != nil {
		t.Fatal(err)
	}
	inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:     source,
			SourceManifest: inspected.Manifest,
			SourceIdentity: inspected.Identity,
		}},
		trackers: &workflowRetainedUploadServiceFake{},
		torrents: issue860MutatingReuse{source: source, artifact: artifact},
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
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
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
	if execution != nil {
		_ = execution.Release()
	}
	if !errors.Is(err, preparedrelease.ErrSourceChanged) {
		t.Fatalf("expected changed source rejection, got %v", err)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("existing reused artifact was removed: %v", err)
	}
}
