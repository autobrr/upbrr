// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/internal/torrentclient"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

type metadataClientRecorder struct {
	calls int
	input api.ClientSubject
	err   error
}

func TestRequestedInfoHashHydrationPreservesAnnounceOnlyTrackerMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "fixture.torrent")
	created, err := mkbrr.Create(mkbrr.CreateOptions{Path: source, OutputPath: artifact})
	if err != nil {
		t.Fatal(err)
	}
	hash := created.InfoHash
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if r.URL.Query().Get("hashes") != hash {
				t.Error("hydration did not forward the requested infohash")
			}
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        hash,
				Name:        filepath.Base(source),
				ContentPath: source,
				Tracker:     "https://blutopia.cc/announce/redacted",
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		case "/api/v2/torrents/export":
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg := config.Config{
		MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(dir, "db.sqlite")},
		ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit"},
		TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
	}
	clients := torrentclient.NewServiceWithRegistry(cfg, api.NopLogger{}, trackerDataTestRegistry(t))
	service := newTestService(t, &stubRepo{},
		WithConfig(cfg),
		WithMediaInfoExporter(stubMediaInfo{}),
		WithSceneDetector(stubSceneDetector{}),
		WithClientDiscovery(clientdiscovery.New(clients, api.NopLogger{})),
		WithTrackerDataLookup(&stubTrackerLookup{}),
	)
	state, err := service.HydratePrivateResources(t.Context(), testCollectionRequest(t, api.Request{
		SourcePath: source, TorrentOverrides: api.TorrentOverrides{InfoHash: new(hash)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if state.InfoHash != hash || state.DiscoveredTorrentPath == "" || state.ClientEvidence.Policy.InfoHash != hash ||
		len(state.TrackerIDs) != 0 || !slices.Contains(state.MatchedEvidenceTrackers, "BLU") {
		t.Fatalf("requested torrent evidence changed during hydration: %+v", state.ClientEvidence)
	}
	record, _, _, err := service.lookupTrackerData(t.Context(), state, "BLU", time.Now())
	if err != nil || !record.Matched || record.InfoHash != hash {
		t.Fatalf("announce-only tracker lost its metadata match: record=%+v err=%v", record, err)
	}
}

func (*metadataClientRecorder) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (*metadataClientRecorder) InjectionDestinationFingerprint(api.ClientSubject, api.TorrentResult) (api.WorkflowFingerprint, error) {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
}

func (c *metadataClientRecorder) SearchPathedTorrents(_ context.Context, input api.ClientSubject) (api.ClientSearchResult, error) {
	c.calls++
	c.input = input
	return api.ClientSearchResult{
		InfoHash:            "client-hash",
		TorrentPath:         "Example.Release.2026.torrent",
		TorrentDataVerified: true,
		TrackerIDs:          map[string]string{"btn": "client-id", "aither": "aither-id"},
		FoundTrackerMatch:   true,
		TorrentComments:     []api.TorrentMatch{{Name: "Example.Release.2026"}},
		PieceSizeConstraint: "4194304",
		FoundPreferredPiece: "4194304",
		MatchedTrackers:     []string{"BTN", "AITHER"},
	}, c.err
}

func TestClientDiscoveryProgressPreservesUnavailableAndSkippedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		clients *clientdiscovery.Module
		skip    bool
		want    api.PreparationProgressStatus
	}{
		{name: "missing module", want: api.PreparationProgressPartial},
		{
			name: "requested skip without module",
			skip: true,
			want: api.PreparationProgressCompleted,
		},
		{
			name:    "failed search",
			clients: clientdiscovery.New(&metadataClientRecorder{err: errors.New("synthetic client failure")}, api.NopLogger{}),
			want:    api.PreparationProgressPartial,
		},
		{
			name:    "requested skip",
			clients: clientdiscovery.New(nil, api.NopLogger{}),
			skip:    true,
			want:    api.PreparationProgressCompleted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var updates []api.PreparationProgressUpdate
			ctx := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { updates = append(updates, u) })
			service := &Service{clients: tc.clients}
			state, err := collectPreparationStage(ctx, api.PreparationPhaseClientDiscovery, func(ctx context.Context) (preparationstate.State, error) {
				return service.collectClientEvidence(ctx, api.PrepareInput{Search: api.ClientSearchPolicy{Skip: tc.skip}}, preparationstate.State{SourcePath: "Example.Release.2026.mkv"})
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.skip && state.ClientEvidence.Disposition != preparationstate.ClientEvidenceDispositionSkipped {
				t.Fatalf("explicit skip disposition = %s", state.ClientEvidence.Disposition)
			}
			if got := updates[len(updates)-1]; got.Status != tc.want {
				t.Fatalf("client discovery terminal outcome = %#v, want %s", got, tc.want)
			}
		})
	}
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
	if state.InfoHash != "client-hash" || state.DiscoveredTorrentPath != "Example.Release.2026.torrent" ||
		!state.ClientEvidence.Result.TorrentDataVerified || !state.FoundTrackerMatch {
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

func TestHydrateClientEvidenceSearchesManifestFilesOnce(t *testing.T) {
	t.Parallel()

	client := &metadataClientRecorder{}
	service := &Service{clients: clientdiscovery.New(client, api.NopLogger{})}
	force := true
	snapshot, err := service.HydrateClientEvidence(context.Background(), preparationstate.Request{
		Input: api.PrepareInput{
			Search:   api.ClientSearchPolicy{},
			Controls: api.PreparationControls{ForceRecheck: &force},
		},
		Manifest: api.SourceManifest{
			SourcePath: "C:\\media\\Example.Release.2026",
			Entries: []api.SourceManifestEntry{
				{Path: "video.mkv", Type: api.SourceEntryTypeFile},
				{Path: "playlist.mpls", Type: api.SourceEntryTypePlaylist},
				{Path: "BDMV", Type: api.SourceEntryTypeDirectory},
			},
		},
		Layout: sourcelayout.Layout{DiscType: "BDMV"},
	})
	if err != nil {
		t.Fatalf("hydrate client evidence: %v", err)
	}
	if client.calls != 1 || !slices.Equal(client.input.FileList, []string{"video.mkv", "playlist.mpls"}) || client.input.DiscType != "BDMV" {
		t.Fatalf("hydration search calls=%d input=%#v", client.calls, client.input)
	}
	if snapshot.Disposition != preparationstate.ClientEvidenceDispositionSearched || !snapshot.ForcedRecheck ||
		snapshot.Result.InfoHash != "client-hash" || snapshot.Result.TorrentDataVerified {
		t.Fatalf("hydrated snapshot = %#v", snapshot)
	}
}

type pipelineClientRecorder struct {
	calls int
}

func (*pipelineClientRecorder) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (*pipelineClientRecorder) InjectionDestinationFingerprint(api.ClientSubject, api.TorrentResult) (api.WorkflowFingerprint, error) {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
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
	service := newTestService(t,
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

	enrichment := testCollectionRequest(t, api.Request{SourcePath: sourcePath})
	enrichment.RetainedClientEvidence = &state.ClientEvidence
	enriched, err := service.CollectPreparationEvidence(t.Context(), enrichment)
	if err != nil {
		t.Fatalf("enrich canonical evidence: %v", err)
	}
	if client.calls != 1 || enriched.TrackerIDs["ant"] != "client-release-id" ||
		enriched.ClientEvidence.Result.TrackerIDs["ant"] != "client-release-id" {
		t.Fatalf("enrichment lost retained evidence or searched again: calls=%d trackers=%v evidence=%#v", client.calls, enriched.TrackerIDs, enriched.ClientEvidence)
	}

	enrichment.RetainedClientEvidence = &preparationstate.ClientEvidenceSnapshot{Disposition: preparationstate.ClientEvidenceDispositionUnavailable}
	var retained []api.PreparationProgressUpdate
	retainedCtx := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { retained = append(retained, u) })
	if _, err := service.CollectPreparationEvidence(retainedCtx, enrichment); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("retained unavailable evidence triggered %d searches", client.calls)
	}
	if !slices.ContainsFunc(retained, func(u api.PreparationProgressUpdate) bool {
		return u.Phase == api.PreparationPhaseClientDiscovery && u.Status == api.PreparationProgressPartial
	}) {
		t.Fatal("retained unavailable client evidence was reported as complete")
	}
}
