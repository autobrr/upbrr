// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/pkg/api"
)

type metadataClientRecorder struct {
	calls int
	input api.ClientSubject
}

func (*metadataClientRecorder) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (c *metadataClientRecorder) SearchPathedTorrents(_ context.Context, input api.ClientSubject) (api.ClientSearchResult, error) {
	c.calls++
	c.input = input
	return api.ClientSearchResult{
		InfoHash:            "client-hash",
		TorrentPath:         "Example.Release.2026.torrent",
		TrackerIDs:          map[string]string{"btn": "client-id", "aither": "aither-id"},
		FoundTrackerMatch:   true,
		TorrentComments:     []api.TorrentMatch{{Name: "Example.Release.2026"}},
		PieceSizeConstraint: "4194304",
		FoundPreferredPiece: "4194304",
		MatchedTrackers:     []string{"BTN", "AITHER"},
	}, nil
}

func TestCollectClientEvidencePrecedesTrackerCandidatesAndPreservesExplicitIDs(t *testing.T) {
	t.Parallel()

	client := &metadataClientRecorder{}
	service := &Service{clients: clientdiscovery.New(client, api.NopLogger{})}
	state, err := service.collectClientEvidence(context.Background(), api.PrepareInput{
		Instructions: api.ReleaseFactInstructions{TrackerIDs: map[string]string{"btn": "explicit-id"}},
		Search:       api.ClientSearchPolicy{},
	}, preparationstate.State{
		SourcePath: "Example.Release.2026.mkv",
		FileList:   []string{"Example.Release.2026.mkv"},
		TrackerIDs: map[string]string{"btn": "explicit-id"},
	})
	if err != nil {
		t.Fatalf("collect client evidence: %v", err)
	}
	if client.calls != 1 || client.input.SourcePath != "Example.Release.2026.mkv" {
		t.Fatalf("client calls=%d input=%#v", client.calls, client.input)
	}
	if state.TrackerIDs["btn"] != "explicit-id" || state.TrackerIDs["aither"] != "aither-id" {
		t.Fatalf("tracker IDs = %#v", state.TrackerIDs)
	}
	if state.InfoHash != "client-hash" || state.DiscoveredTorrentPath != "Example.Release.2026.torrent" || !state.FoundTrackerMatch {
		t.Fatalf("client state = %#v", state)
	}
	if state.PieceSizeConstraint != "4194304" || state.FoundPreferredPiece != "4194304" || len(state.TorrentComments) != 1 {
		t.Fatalf("retained evidence = %#v", state)
	}
	candidates := normalizeTrackers(resolveTrackerCandidates(state))
	if !slices.Equal(candidates, []string{"AITHER", "BTN"}) {
		t.Fatalf("tracker candidates = %#v", candidates)
	}
}

type pipelineClientRecorder struct {
	calls int
}

func (*pipelineClientRecorder) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (c *pipelineClientRecorder) SearchPathedTorrents(context.Context, api.ClientSubject) (api.ClientSearchResult, error) {
	c.calls++
	return api.ClientSearchResult{TrackerIDs: map[string]string{"ant": "client-release-id"}}, nil
}

func TestCanonicalEvidencePipelineUsesClientTrackerIDBeforeProviderResolution(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &pipelineClientRecorder{}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"ANT": {TMDBID: 1234567, TrackerID: "client-release-id"},
	}}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"ANT": {APIKey: "synthetic-api-key"},
	}}}
	service := NewService(
		&fakeRepo{},
		WithConfig(cfg),
		WithMediaInfoExporter(stubMediaInfo{}),
		WithSceneDetector(stubSceneDetector{}),
		WithClientDiscovery(clientdiscovery.New(client, api.NopLogger{})),
		WithTrackerDataLookup(lookup),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Release", Year: 2026}}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	var progress []api.PreparationProgressUpdate
	ctx := api.WithPreparationProgressReporter(context.Background(), func(update api.PreparationProgressUpdate) {
		progress = append(progress, update)
	})
	state, err := service.CollectPreparationEvidence(ctx, testCollectionRequest(t, api.Request{SourcePath: sourcePath}))
	if err != nil {
		t.Fatalf("collect canonical evidence: %v", err)
	}
	if client.calls != 1 || !slices.Equal(lookup.Calls(), []string{"ANT"}) {
		t.Fatalf("client calls=%d tracker calls=%v", client.calls, lookup.Calls())
	}
	if state.Identity.TMDBID != 1234567 || state.ProviderMetadata.TMDB == nil || state.ProviderMetadata.TMDB.TMDBID != 1234567 {
		t.Fatalf("identity/provider did not use client-derived tracker evidence: identity=%#v provider=%#v", state.Identity, state.ProviderMetadata.TMDB)
	}
	var completed []api.PreparationProgressPhase
	for _, update := range progress {
		if update.Status == api.PreparationProgressCompleted {
			completed = append(completed, update.Phase)
		}
	}
	wantCompleted := []api.PreparationProgressPhase{
		api.PreparationPhaseSourceEvidence,
		api.PreparationPhaseClientDiscovery,
		api.PreparationPhaseTrackerEvidence,
		api.PreparationPhaseMediaInfoIdentity,
		api.PreparationPhaseArrIdentity,
		api.PreparationPhaseExternalIdentity,
		api.PreparationPhaseMediaFacts,
	}
	if !slices.Equal(completed, wantCompleted) {
		t.Fatalf("completed progress phases=%v, want %v", completed, wantCompleted)
	}
}
