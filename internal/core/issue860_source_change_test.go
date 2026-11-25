// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

func readyWorkflowTrackers(ids ...api.TrackerID) (api.TrackerReleaseProjectionSet, api.DupeAssessment) {
	projections := make([]api.TrackerReleaseProjection, 0, len(ids))
	dupes := make([]api.TrackerDupeAssessment, 0, len(ids))
	for _, id := range ids {
		projections = append(projections, api.TrackerReleaseProjection{
			TrackerID:   id,
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		})
		dupes = append(dupes, api.TrackerDupeAssessment{
			TrackerID: id,
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		})
	}
	return api.TrackerReleaseProjectionSet{Projections: projections}, api.DupeAssessment{Results: dupes}
}

func TestWorkflowDiscardsGeneratedTorrentWhenSourceChanges(t *testing.T) {
	t.Parallel()
	for name, partial := range map[string]bool{"full generation": false, "partial extraction": true} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
			if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			var clientCandidates []api.ClientTorrentCandidate
			if partial {
				if err := os.WriteFile(filepath.Join(filepath.Dir(source), "readme.nfo"), []byte("sidecar"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "client.torrent")
				if _, err := mkbrr.Create(mkbrr.CreateOptions{
					Path:       filepath.Dir(source),
					OutputPath: path,
					Quiet:      true,
				}); err != nil {
					t.Fatal(err)
				}
				mi, err := metainfo.LoadFromFile(path)
				if err != nil {
					t.Fatal(err)
				}
				clientCandidates = []api.ClientTorrentCandidate{{Path: path, InfoHash: mi.HashInfoBytes().String()}}
			}
			inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			tmpRoot := t.TempDir()
			torrents := torrent.NewService(nil, tmpRoot)
			builder := workflowUploadPlanBuilder{
				resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
					SourcePath:              source,
					SourceManifest:          inspected.Manifest,
					SourceIdentity:          inspected.Identity,
					ClientTorrentCandidates: clientCandidates,
				}},
				trackers: &workflowRetainedUploadServiceFake{},
				torrents: torrents,
			}
			ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
			var mutateOnce sync.Once
			var mutationErr error
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if (!partial && update.Message != "Hashing complete") || (partial && (update.Message != "Hashing final partial torrent piece" || update.CompletedPieces != 1)) {
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
			projections, dupes := readyWorkflowTrackers("EXAMPLE")
			_, execution, err := builder.Build(ctx, projections, dupes,
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
		})
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
	projections, dupes := readyWorkflowTrackers("EXAMPLE")
	_, execution, err := builder.Build(ctx, projections, dupes,
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
	projections, dupes := readyWorkflowTrackers("EXAMPLE")
	_, execution, err := builder.Build(ctx, projections, dupes,
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

type torrentServiceAfterCreate struct {
	api.TorrentService
	deferPublication bool
	afterCreate      func(*api.TorrentResult)
}

func (s torrentServiceAfterCreate) Create(ctx context.Context, subject api.TorrentSubject) (api.TorrentResult, error) {
	if s.deferPublication {
		subject.DeferGenerationPublication = true
	}
	result, err := s.TorrentService.Create(ctx, subject)
	if err != nil {
		return api.TorrentResult{}, fmt.Errorf("create test torrent: %w", err)
	}
	if result.PendingPublication != nil {
		s.afterCreate(&result)
	}
	return result, nil
}

type torrentPublicationBeforePublish struct {
	api.TorrentPublication
	beforePublish func()
}

func (p torrentPublicationBeforePublish) Publish() (string, error) {
	p.beforePublish()
	path, err := p.TorrentPublication.Publish()
	if err != nil {
		return "", fmt.Errorf("publish test torrent: %w", err)
	}
	return path, nil
}

func TestWorkflowMultiTrackerTorrentProgressClosesScopes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		cancelAt    string
		wantStatus  string
		wantMessage string
		wantErr     error
	}{
		{
			name:        "stable source",
			wantStatus:  "completed",
			wantMessage: "Torrent ready",
		},
		{
			name:        "changed source",
			wantStatus:  "failed",
			wantMessage: "Source changed during torrent generation",
			wantErr:     preparedrelease.ErrSourceChanged,
		},
		{
			name:        "canceled before final stability check",
			cancelAt:    "stability",
			wantStatus:  "canceled",
			wantMessage: "Torrent preparation canceled",
			wantErr:     context.Canceled,
		},
		{
			name:        "canceled during publication",
			cancelAt:    "publication",
			wantStatus:  "canceled",
			wantMessage: "Torrent preparation canceled",
			wantErr:     context.Canceled,
		},
		{
			name:        "canceled before publication without a manifest",
			cancelAt:    "before publication",
			wantStatus:  "canceled",
			wantMessage: "Torrent preparation canceled",
			wantErr:     context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "Example.Multi.2026.mkv")
			if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(dir, "Other.2026.mkv")
			if err := os.WriteFile(other, []byte("other-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			incompatible := filepath.Join(dir, "incompatible.torrent")
			if _, err := mkbrr.Create(mkbrr.CreateOptions{Path: other, OutputPath: incompatible}); err != nil {
				t.Fatal(err)
			}
			inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tmpRoot := t.TempDir()
			var torrents api.TorrentService = torrent.NewService(nil, tmpRoot)
			switch tc.cancelAt {
			case "stability":
				torrents = torrentServiceAfterCreate{TorrentService: torrents, afterCreate: func(*api.TorrentResult) { cancel() }}
			case "publication":
				torrents = torrentServiceAfterCreate{TorrentService: torrents, afterCreate: func(result *api.TorrentResult) {
					result.PendingPublication = torrentPublicationBeforePublish{
						TorrentPublication: result.PendingPublication,
						beforePublish:      cancel,
					}
				}}
			case "before publication":
				torrents = torrentServiceAfterCreate{
					TorrentService:   torrents,
					deferPublication: true,
					afterCreate: func(result *api.TorrentResult) {
						result.PendingPublication = torrentPublicationBeforePublish{
							TorrentPublication: result.PendingPublication,
							beforePublish:      func() { t.Error("Build called Publish after cancellation") },
						}
						cancel()
					},
				}
			}
			builder := workflowUploadPlanBuilder{
				resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
					SourcePath:        source,
					SourceManifest:    inspected.Manifest,
					SourceIdentity:    inspected.Identity,
					ClientTorrentPath: incompatible,
				}},
				trackers: &workflowRetainedUploadServiceFake{},
				torrents: torrents,
			}
			if tc.cancelAt != "before publication" {
				ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
			}
			var (
				mu         sync.Mutex
				lastUpdate = make(map[string]api.UploadProgressUpdate)
				mutateOnce sync.Once
				mutateErr  error
			)
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.Task != "torrent" {
					return
				}
				mu.Lock()
				lastUpdate[update.Tracker] = update
				mu.Unlock()
				if tc.wantStatus == "failed" && update.Message == "Hashing complete" {
					mutateOnce.Do(func() {
						mutateErr = os.WriteFile(source, []byte("source-evil"), 0o600)
						if mutateErr == nil {
							changed := time.Now().Add(time.Hour)
							mutateErr = os.Chtimes(source, changed, changed)
						}
					})
				}
			})
			projections, dupes := readyWorkflowTrackers("TL", "BHD")
			_, execution, err := builder.Build(ctx, projections, dupes,
				workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{},
				releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true}, time.Now())
			if execution != nil {
				_ = execution.Release()
			}
			if mutateErr != nil {
				t.Fatal(mutateErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Build error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantStatus == "canceled" && errors.Is(err, preparedrelease.ErrSourceChanged) {
				t.Fatalf("cancellation was classified as source drift: %v", err)
			}
			if lastUpdate["TL"].Status != tc.wantStatus || lastUpdate["TL"].Message != tc.wantMessage || lastUpdate[""].Status != "completed" {
				t.Fatalf("expected TL torrent scope %q (%q) and shared check completed, got %+v", tc.wantStatus, tc.wantMessage, lastUpdate)
			}
			for tracker, update := range lastUpdate {
				if update.Status == "running" {
					t.Fatalf("torrent progress scope %q remained running after Build: %+v", tracker, lastUpdate)
				}
			}
			if tc.wantStatus == "canceled" {
				path, err := torrent.TempTorrentPath(tmpRoot, source)
				if err != nil {
					t.Fatal(err)
				}
				for _, pattern := range []string{".generating-*.torrent", filepath.Join("base-torrents", "*.torrent")} {
					files, err := filepath.Glob(filepath.Join(filepath.Dir(path), pattern))
					if err != nil || len(files) > 0 {
						t.Fatalf("canceled publication left torrent artifacts: %v, err=%v", files, err)
					}
				}
			}
		})
	}
}

type discoveryHookClient struct {
	issue860Client
	onSearch func()
}

func (c *discoveryHookClient) SearchPathedTorrents(ctx context.Context, subject api.ClientSubject) (api.ClientSearchResult, error) {
	if c.onSearch != nil {
		c.onSearch()
	}
	return c.issue860Client.SearchPathedTorrents(ctx, subject)
}

func TestPrepareWorkflowTorrentClosesProbeScopeAcrossDiscoveryAndRetry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		trackers          []string
		coldCache         bool
		discoverCandidate bool
		searchErr         error
		wantScope         string
		wantStatus        string
		wantMessage       string
	}{
		{
			name:        "recheck error",
			trackers:    []string{"PTP"},
			searchErr:   internalerrors.ErrClientRecheckFailed,
			wantScope:   "PTP",
			wantStatus:  "failed",
			wantMessage: "Torrent search failed",
		},
		{
			name:        "recheck timed out with live workflow context",
			trackers:    []string{"PTP"},
			searchErr:   fmt.Errorf("%w: %w", internalerrors.ErrClientRecheckFailed, context.DeadlineExceeded),
			wantScope:   "PTP",
			wantStatus:  "failed",
			wantMessage: "Torrent search failed",
		},
		{
			name:        "canceled during discovery",
			trackers:    []string{"PTP"},
			searchErr:   context.Canceled,
			wantScope:   "PTP",
			wantStatus:  "canceled",
			wantMessage: "Torrent preparation canceled",
		},
		{
			name:       "shared probe candidate disappeared before retry",
			trackers:   []string{"PTP", "TL"},
			wantStatus: "skipped",
		},
		{
			name:        "cold cache recheck timeout with live workflow context",
			trackers:    []string{" ptp "},
			coldCache:   true,
			searchErr:   fmt.Errorf("%w: %w", internalerrors.ErrClientRecheckFailed, context.DeadlineExceeded),
			wantScope:   "PTP",
			wantStatus:  "failed",
			wantMessage: "Torrent search failed",
		},
		{
			name:        "cold cache shared search canceled by workflow",
			trackers:    []string{"PTP", "TL"},
			coldCache:   true,
			searchErr:   context.Canceled,
			wantStatus:  "canceled",
			wantMessage: "Torrent preparation canceled",
		},
		{
			name:              "cold cache retry reuses discovered torrent",
			trackers:          []string{"PTP"},
			coldCache:         true,
			discoverCandidate: true,
			wantScope:         "PTP",
			wantStatus:        "completed",
		},
		{
			name:       "cold cache shared retry skips all trackers",
			trackers:   []string{"PTP", "TL"},
			coldCache:  true,
			wantStatus: "skipped",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, candidates := issue860CandidateFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var updates []api.UploadProgressUpdate
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.Task == "torrent" {
					updates = append(updates, update)
				}
			})
			clients := &discoveryHookClient{err: tc.searchErr}
			clients.onSearch = func() {
				if len(updates) == 0 || (tc.coldCache && len(updates) != 1) {
					t.Fatalf("unexpected torrent progress at discovery entry: %+v", updates)
				}
				if !tc.coldCache && updates[len(updates)-1].Message == "Searching for reusable torrents" {
					t.Fatalf("discovery reopened the running probe scope: %+v", updates)
				}
				if errors.Is(tc.searchErr, context.Canceled) {
					cancel()
				} else if !tc.coldCache && tc.searchErr == nil {
					if err := os.Remove(candidates[0].Path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.discoverCandidate {
				clients.result.TorrentCandidates = candidates[1:]
			}

			builder := workflowUploadPlanBuilder{
				torrents: torrent.NewService(api.NopLogger{}, t.TempDir()),
				clients:  clients,
			}
			input := api.TorrentSubject{
				SourcePath:       source,
				SourceSize:       11,
				Trackers:         tc.trackers,
				ForceRecheck:     true,
				TorrentOverrides: api.TorrentOverrides{NoHash: new(true)},
			}
			if !tc.coldCache {
				input.ClientTorrentPath = candidates[0].Path
				input.ClientTorrentInfoHash = candidates[0].InfoHash
			}
			result, err := builder.prepareWorkflowTorrent(ctx, api.UploadSubject{ClientSearchPerformed: true}, input)
			if !errors.Is(err, tc.searchErr) {
				t.Fatalf("prepareWorkflowTorrent error = %v, want %v", err, tc.searchErr)
			}
			if tc.wantStatus == "skipped" && len(result.SkippedTrackers) != len(tc.trackers) {
				t.Fatalf("expected unavailable candidate to skip all trackers: %+v", result)
			}
			if tc.discoverCandidate && result.TrackerArtifacts[tc.wantScope].InfoHash != candidates[1].InfoHash {
				t.Fatalf("retry did not reuse the discovered torrent: %+v", result)
			}
			if len(updates) < 2 || updates[0].Status != "running" {
				t.Fatalf("expected a running scope before discovery, got %+v", updates)
			}
			last := updates[len(updates)-1]
			if last.Tracker != tc.wantScope || last.Status != tc.wantStatus || (tc.wantMessage != "" && last.Message != tc.wantMessage) {
				t.Fatalf("expected terminal %q for scope %q, got %+v", tc.wantStatus, tc.wantScope, updates)
			}
			for _, update := range updates[:len(updates)-1] {
				if update.Tracker != tc.wantScope || update.Status != "running" {
					t.Fatalf("torrent scope emitted a duplicate or premature terminal outcome: %+v", updates)
				}
			}
		})
	}
}

func TestWorkflowTorrentDiscoveryRetryPersistsVerificationProgress(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	const size = 1 << 20
	if err := os.WriteFile(source, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	createCandidate := func(input, name string, pieceExp uint) api.ClientTorrentCandidate {
		t.Helper()
		candidate, err := mkbrr.Create(mkbrr.CreateOptions{
			Path:           input,
			OutputPath:     filepath.Join(t.TempDir(), name),
			PieceLengthExp: &pieceExp,
			MaxPieceLength: &pieceExp,
			Quiet:          true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return api.ClientTorrentCandidate{Path: candidate.Path, InfoHash: candidate.InfoHash}
	}
	// TL prefers the aligned wrapper to the known exact candidate. PTP
	// rejects both piece sizes and needs the exact candidate from discovery.
	known := createCandidate(source, "large.torrent", 22)
	wrapper := createCandidate(dir, "wrapped.torrent", 19)
	discovered := createCandidate(source, "compatible.torrent", 15)
	var discoveryStarted, probeRecheckedWrapper atomic.Bool
	clients := &discoveryHookClient{
		result:   api.ClientSearchResult{TorrentCandidates: []api.ClientTorrentCandidate{discovered}},
		onSearch: func() { discoveryStarted.Store(true) },
	}
	builder := workflowUploadPlanBuilder{torrents: torrent.NewService(nil, t.TempDir()), clients: clients}
	retryStarted := make(chan struct{})
	resumeRetry := make(chan struct{})
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(resumeRetry) }) }
	defer resume()
	module, err := releaseworkflow.New(releaseworkflow.NewMemoryRepository(), releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{
			PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				reportCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
					api.EmitUploadProgress(ctx, update)
					if update.Message != "Verifying torrent pieces" {
						return
					}
					if !discoveryStarted.Load() && update.Tracker == "TL" {
						probeRecheckedWrapper.Store(true)
					}
					if discoveryStarted.Load() && update.Tracker == "" && update.TotalPieces == size/(1<<15) {
						select {
						case retryStarted <- struct{}{}:
							<-resumeRetry
						case <-resumeRetry:
						}
					}
				})
				result, err := builder.prepareWorkflowTorrent(reportCtx, api.UploadSubject{}, api.TorrentSubject{
					SourcePath:              input.SourcePath,
					SourceSize:              size,
					Trackers:                []string{"TL", "PTP"},
					ClientTorrentCandidates: []api.ClientTorrentCandidate{known, wrapper},
					ForceRecheck:            true,
					TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
				})
				if err != nil {
					return api.PrepareResult{}, err
				}
				if result.TrackerArtifacts["PTP"].InfoHash != discovered.InfoHash {
					return api.PrepareResult{}, errors.New("retry did not select the discovered PTP torrent")
				}
				return api.PrepareResult{Release: api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.SourcePath}}}, nil
			},
			DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				return api.PreparedReleaseDisplay{}, nil
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const owner = "torrent-progress-owner"
	created, err := module.Execute(ctx, owner, releaseworkflow.CreateWorkflowCommand{})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := module.Start(ctx, owner, releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: source},
		IdempotencyKey:   "torrent-discovery-retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-retryStarted:
	case <-ctx.Done():
		t.Fatal("discovery retry did not start verification")
	}
	if !probeRecheckedWrapper.Load() {
		t.Fatal("probe did not verify TL's wrapper before discovery")
	}
	waitForProgress := func(want api.StageStatus, matches func(api.WorkflowOperationItem) bool) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			status, err := module.Operation(ctx, owner, operation.WorkflowID, operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.Status == want && slices.ContainsFunc(status.Items, matches) {
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatalf("missing %s torrent progress: %+v", want, status.Items)
			}
		}
	}
	waitForProgress(api.StageStatusRunning, func(item api.WorkflowOperationItem) bool {
		return item.ID == "torrent" && item.Status == api.StageStatusRunning && item.Message == "Verifying torrent pieces" && item.Total == size/(1<<15)
	})
	resume()
	waitForProgress(api.StageStatusCompleted, func(item api.WorkflowOperationItem) bool {
		return item.ID == "torrent" && item.Status == api.StageStatusCompleted
	})
}
