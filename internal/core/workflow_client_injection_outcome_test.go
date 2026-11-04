// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/torrentclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func newInjectionOutcomeReporter(t *testing.T) *dryRunInjectionReporter {
	t.Helper()
	repo, err := db.Open(filepath.Join(t.TempDir(), "injection-outcome.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), api.ReleaseWorkflowStateRecord{
		OwnerID:             "owner",
		WorkflowID:          "workflow",
		Revision:            1,
		Status:              api.WorkflowStatusActive,
		CreationFingerprint: "create",
		Payload:             []byte(`{"revision":1}`),
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatal(err)
	}
	return &dryRunInjectionReporter{repository: repo}
}

func TestDryRunPreparationDecisionContinuesClientInjection(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"continue", "skip option", "live review", "live test", "no destination", "client failure", "unknown outcome", "another decision", "skip preparation"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			torrentPath := filepath.Join(root, "prepared.torrent")
			if err := os.WriteFile(torrentPath, []byte("prepared torrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			action := api.RequiredAction{Kind: api.RequiredActionResolveTrackerPreparation, Prompt: "Continue preparation?"}
			service := &workflowRetainedUploadServiceFake{
				actions: map[api.TrackerID][]api.RequiredAction{"ALPHA": {action}},
				failures: map[api.TrackerID]trackers.TrackerFailure{"ALPHA": {
					Tracker: "ALPHA",
					Code:    "decision_required",
					Message: "A preparation decision is required.",
				}},
				resolved: map[string]trackers.RetainedTrackerPreparation{"ALPHA": {Tracker: "ALPHA", TorrentPath: torrentPath}},
			}
			client := &dryRunClientService{}
			builder := workflowUploadPlanBuilder{
				resolver: &workflowDescriptionResolverFake{},
				trackers: service,
				clients:  client,
			}
			options := releaseworkflow.UploadPlanBuildOptions{DryRun: true}
			wantStatus, wantCode, wantCalls := api.StageStatusCompleted, api.OperationFailureCode(""), 1
			switch name {
			case "skip option":
				options.NoSeed, wantStatus, wantCalls = true, api.StageStatusSkipped, 0
			case "live review":
				options.ReviewOnly, wantStatus, wantCalls = true, api.StageStatusSkipped, 0
			case "live test":
				builder.liveTest, wantStatus, wantCalls = &api.LiveTestPolicy{}, api.StageStatusSkipped, 0
			case "no destination":
				builder.clients, wantStatus, wantCalls = torrentclient.NewService(config.Config{}, nil), api.StageStatusSkipped, 0
			case "client failure":
				client.injectErr, wantStatus, wantCode = errors.New("synthetic client failure"), api.StageStatusFailed, api.OperationFailureDryRunClientInjection
			case "unknown outcome":
				client.injectErr, wantStatus, wantCode = api.ErrReleaseWorkflowEffectOutcomeUnknown, api.StageStatusFailed, api.OperationFailureUnknownOutcome
			case "another decision":
				preparation := service.resolved["ALPHA"]
				preparation.Preview.RequiredActions = []api.RequiredAction{action}
				service.resolved["ALPHA"] = preparation
				wantStatus, wantCalls = api.StageStatusPending, 0
			case "skip preparation":
				preparation := service.resolved["ALPHA"]
				preparation.Failure = &trackers.TrackerFailure{Code: trackers.PreparationFailureCodeSkipped}
				service.resolved["ALPHA"] = preparation
				wantStatus, wantCalls = api.StageStatusSkipped, 0
			}
			reporter := newInjectionOutcomeReporter(t)
			ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
			build := func() releaseworkflow.RetainedUploadExecution {
				t.Helper()
				plan, execution, err := builder.Build(ctx,
					api.TrackerReleaseProjectionSet{
						ExecutionMode: api.WorkflowExecutionModeNormal,
						ReleaseRef:    api.ReleaseRef{SourcePath: root, Generation: 1},
						Projections: []api.TrackerReleaseProjection{{
							TrackerID:         "ALPHA",
							UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
							Readiness:         api.ReadinessStatusReady,
							UploadReady:       true,
						}},
					},
					api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
						TrackerID: "ALPHA",
						Decision:  api.DupeDecisionNoMatch,
						Status:    api.StageStatusCompleted,
					}}},
					workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{Options: api.UploadOptions{SkipAutoTorrent: true}}, options, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = execution.Release() })
				pending := api.StageStatusPending
				if options.NoSeed || options.ReviewOnly || builder.liveTest != nil {
					pending = api.StageStatusSkipped
				}
				if len(plan.Trackers) != 1 || plan.Trackers[0].ClientInjectionStatus != pending {
					t.Fatalf("unresolved preparation client state = %s, want %s", plan.Trackers[0].ClientInjectionStatus, pending)
				}
				return execution
			}
			execution := build()
			if len(client.injections) != 0 || reporter.attempt != 0 {
				t.Fatal("pending preparation dispatched a client action")
			}
			for attempt := range 2 {
				resolved, err := execution.ResolveAction(ctx, "ALPHA", action.Kind, true)
				if err != nil || resolved.ClientInjectionStatus != wantStatus || resolved.ClientFailureCode != wantCode || len(client.injections) != wantCalls {
					t.Fatalf("resolution %d: status=%s code=%s calls=%d err=%v", attempt, resolved.ClientInjectionStatus, resolved.ClientFailureCode, len(client.injections), err)
				}
				if wantStatus == api.StageStatusFailed && (resolved.Eligible || resolved.Status != api.StageStatusFailed || len(resolved.Failures) != 1) {
					t.Fatalf("client failure was reported as ready: %#v", resolved)
				}
				if name == "client failure" {
					break // A definite failure may retry after a new explicit request.
				}
				if name == "continue" {
					execution = build()
				}
			}
		})
	}
}

func TestWorkflowClientInjectionRequiresEnabledDestination(t *testing.T) {
	t.Parallel()
	for _, selection := range []string{"no clients", "no selection", "disabled selector", "disabled client", "watch folder", "URL-only watch folder"} {
		t.Run(selection, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			torrentPath := filepath.Join(root, "Example.Release.2026.torrent")
			writeWorkflowRegisteredTorrent(t, torrentPath, "ALPHA")
			cfg := config.Config{TorrentClients: map[string]config.TorrentClientConfig{
				"watch": {Type: "watch", WatchFolder: root},
				"other": {Type: "disabled"},
			}}
			switch selection {
			case "no clients":
				cfg.TorrentClients = nil
			case "disabled selector":
				cfg.ClientSetup.DefaultClient = "none"
			case "disabled client":
				cfg.ClientSetup.DefaultClient = "other"
			case "watch folder", "URL-only watch folder":
				watch := filepath.Join(root, "watch")
				if err := os.Mkdir(watch, 0o700); err != nil {
					t.Fatal(err)
				}
				cfg.TorrentClients["watch"] = config.TorrentClientConfig{Type: "watch", WatchFolder: watch}
				cfg.ClientSetup.DefaultClient = "watch"
			}
			clients := torrentclient.NewService(cfg, nil)
			torrent := api.TorrentResult{Path: torrentPath, Tracker: "ALPHA"}
			if selection == "URL-only watch folder" {
				torrent.Path, torrent.URL = "", "https://registered.example/download"
			}
			if selection != "watch folder" {
				// Ordinary callers retain their established no-op behavior.
				if err := clients.Inject(t.Context(), api.ClientSubject{}, torrent); err != nil {
					t.Fatalf("ordinary injection: %v", err)
				}
			}
			reporter := newInjectionOutcomeReporter(t)
			ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
			for range 2 {
				wantStatus := api.StageStatusSkipped
				if selection == "watch folder" {
					wantStatus = api.StageStatusCompleted
				}
				if selection != "URL-only watch folder" { // Prepared dry runs always have a local artifact.
					status, _, code, err := injectWorkflowDryRunClient(ctx, clients, api.ClientSubject{},
						api.TrackerReleaseProjection{TrackerID: "ALPHA"}, torrentPath, 0, 1)
					if err != nil || status != wantStatus || code != "" {
						t.Fatalf("dry-run dispatch status=%s code=%s err=%v", status, code, err)
					}
				}
				live := executeWorkflowClientInjection(ctx, clients, api.ClientSubject{}, "ALPHA", torrent)
				if live.SubmissionStatus != api.StageStatusCompleted || live.ClientInjectionStatus != wantStatus || live.ClientInjected != (selection == "watch folder") {
					t.Fatalf("live dispatch: submission=%s injection=%s injected=%t", live.SubmissionStatus, live.ClientInjectionStatus, live.ClientInjected)
				}
				execution := &workflowUploadExecution{
					clients:    clients,
					crossSeeds: []api.UploadedTorrent{{
						Tracker:     "ALPHA",
						TorrentPath: torrent.Path,
						DownloadURL: torrent.URL,
					}},
				}
				crossSeeds, err := execution.Execute(ctx, nil)
				if err != nil || len(crossSeeds) != 1 {
					t.Fatalf("cross-seed dispatch results=%d err=%v", len(crossSeeds), err)
				}
				if crossSeeds[0].ClientInjectionStatus != wantStatus || crossSeeds[0].CrossSeeded != (selection == "watch folder") {
					t.Fatalf("cross-seed dispatch: status=%s seeded=%t", crossSeeds[0].ClientInjectionStatus, crossSeeds[0].CrossSeeded)
				}
			}
			if selection == "watch folder" {
				if !reporter.deduped {
					t.Fatal("successful dispatch did not retain a succeeded receipt")
				}
				if _, err := os.Stat(filepath.Join(root, "watch", filepath.Base(torrentPath))); err != nil {
					t.Fatalf("watch folder dispatch: %v", err)
				}
			} else if reporter.attempt != 0 {
				t.Fatalf("empty dispatch created %d external effect receipts", reporter.attempt)
			}
		})
	}
}

func TestDryRunNoClientSelectionKeepsTrackerEligible(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	torrentPath := filepath.Join(root, "prepared.torrent")
	if err := os.WriteFile(torrentPath, []byte("prepared torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := workflowUploadPlanBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: &workflowRetainedUploadServiceFake{torrentPaths: map[api.TrackerID]string{"ALPHA": torrentPath}},
		clients:  torrentclient.NewService(config.Config{}, nil),
	}
	plan, execution, err := builder.Build(t.Context(),
		api.TrackerReleaseProjectionSet{
			ExecutionMode: api.WorkflowExecutionModeNormal,
			ReleaseRef:    api.ReleaseRef{SourcePath: root, Generation: 1},
			Projections: []api.TrackerReleaseProjection{{
				TrackerID:         "ALPHA",
				DisplayName:       "Alpha",
				UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			}},
		},
		api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		}}},
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{},
		api.DescriptionInstructions{Options: api.UploadOptions{SkipAutoTorrent: true}},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = execution.Release() }()
	if len(plan.Trackers) != 1 || !plan.Trackers[0].Eligible || plan.Trackers[0].Status != api.StageStatusReady ||
		plan.Trackers[0].ClientInjectionStatus != api.StageStatusSkipped || len(plan.Trackers[0].Failures) != 0 {
		t.Fatalf("no-client selection blocked tracker preparation: %#v", plan.Trackers)
	}
}

func TestWorkflowQbitInjectionVerifiesRequiredTrackers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		body       string
		private    bool
		installed  bool
		readError  bool
		noTrackers bool
	}{
		{name: "unmerged duplicate", body: "Fails."},
		{name: "primary only", body: "Ok."},
		{
			name:    "private duplicate",
			body:    "Fails.",
			private: true,
		},
		{
			name:      "merged duplicate",
			body:      "Fails.",
			installed: true,
		},
		{
			name:      "already present",
			body:      "Ok.",
			installed: true,
			private:   true,
		},
		{
			name:      "fresh add",
			body:      "Ok.",
			installed: true,
		},
		{
			name:      "readback unavailable",
			body:      "Ok.",
			readError: true,
		},
		{
			name:       "trackerless prepared add",
			body:       "Ok.",
			installed:  true,
			noTrackers: true,
		},
	} {
		for _, live := range []bool{false, true} {
			name := test.name + "/dry run"
			if live {
				name = test.name + "/live upload"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				info, err := bencode.Marshal(metainfo.Info{
					Name:        "Example.Release.2026.mkv",
					Length:      1,
					PieceLength: 1,
					Pieces:      make([]byte, 20),
					Private:     &test.private,
				})
				if err != nil {
					t.Fatal(err)
				}
				torrent := &metainfo.MetaInfo{
					InfoBytes:    info,
					Announce:     "https://registered.example/announce",
					AnnounceList: metainfo.AnnounceList{{"https://registered.example/announce"}, {"https://fallback.example/announce"}},
				}
				if test.noTrackers {
					torrent.Announce, torrent.AnnounceList = "", nil
				}
				torrentPath := filepath.Join(root, "registered.torrent")
				file, err := os.OpenFile(torrentPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if err := torrent.Write(file); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				var adds, reads atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v2/auth/login":
						_, _ = w.Write([]byte("Ok."))
					case "/api/v2/torrents/add":
						adds.Add(1)
						w.Header().Set("Content-Type", "text/plain")
						_, _ = w.Write([]byte(test.body))
					case "/api/v2/torrents/info":
						reads.Add(1)
						if r.URL.Query().Get("hashes") != torrent.HashInfoBytes().HexString() {
							t.Error("verification queried a different torrent")
						}
						if test.readError {
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						if test.name == "fresh add" && adds.Load() == 0 {
							_, _ = w.Write([]byte("[]"))
						} else {
							_ = json.NewEncoder(w).Encode([]map[string]string{{"hash": torrent.HashInfoBytes().HexString()}})
						}
					case "/api/v2/torrents/trackers":
						reads.Add(1)
						if r.URL.Query().Get("hash") != torrent.HashInfoBytes().HexString() {
							t.Error("tracker verification queried a different torrent")
						}
						trackers := []map[string]string{{"url": "https://prepared.example/announce"}}
						if test.installed && (test.name == "already present" || adds.Load() > 0) {
							trackers = []map[string]string{{"url": torrent.Announce}, {"url": "https://fallback.example/announce"}}
						}
						if test.name == "primary only" {
							trackers = []map[string]string{{"url": torrent.Announce}}
						}
						_ = json.NewEncoder(w).Encode(trackers)
					default:
						t.Errorf("unexpected client request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(server.Close)
				clients := torrentclient.NewService(config.Config{TorrentClients: map[string]config.TorrentClientConfig{
					"qbit": {Type: "qbit", URL: server.URL},
				}}, nil)
				reporter := newInjectionOutcomeReporter(t)
				ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
				inject := func() (bool, api.OperationFailureCode) {
					t.Helper()
					if live {
						completed, code, _, err := injectWorkflowClientWithFence(ctx, clients,
							api.ClientSubject{SourcePath: filepath.Join(root, "Example.Release.2026.mkv")},
							api.TorrentResult{Path: torrentPath, Tracker: "ALPHA"}, "ALPHA", false)
						if err != nil {
							t.Fatal(err)
						}
						return completed, code
					}
					status, _, code, err := injectWorkflowDryRunClient(ctx, clients,
						api.ClientSubject{SourcePath: filepath.Join(root, "Example.Release.2026.mkv")},
						api.TrackerReleaseProjection{TrackerID: "ALPHA"}, torrentPath, 0, 1)
					if err != nil {
						t.Fatal(err)
					}
					return status == api.StageStatusCompleted, code
				}
				completed, code := inject()
				if completed != test.installed || (!test.installed && code != api.OperationFailureUnknownOutcome) {
					t.Fatalf("injection completed=%t code=%s", completed, code)
				}
				if reads.Load() == 0 {
					t.Fatal("client success was inferred without reading installed state")
				}
				priorAdds := adds.Load()
				completed, code = inject()
				if completed != test.installed || adds.Load() != priorAdds || (!test.installed && code != api.OperationFailureUnknownOutcome) {
					t.Fatalf("replay repeated or misreported injection: completed=%t code=%s adds=%d prior=%d", completed, code, adds.Load(), priorAdds)
				}
			})
		}
	}
}

func TestDryRunPartialClientInjectionRemainsFenced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	watch := filepath.Join(root, "watch")
	if err := os.Mkdir(watch, 0o700); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(root, "prepared.torrent")
	if err := os.WriteFile(torrentPath, []byte("prepared torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{TorrentClients: map[string]config.TorrentClientConfig{
		"first":  {Type: "watch", WatchFolder: watch},
		"second": {Type: "qbit"},
	}}
	cfg.ClientSetup.InjectClients = []string{"first", "second"}
	clients := torrentclient.NewService(cfg, nil)
	reporter := newInjectionOutcomeReporter(t)
	ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
	for attempt := range 2 {
		status, _, code, err := injectWorkflowDryRunClient(ctx, clients, api.ClientSubject{},
			api.TrackerReleaseProjection{TrackerID: "ALPHA"}, torrentPath, 0, 1)
		if err != nil || status != api.StageStatusFailed || code != api.OperationFailureUnknownOutcome {
			t.Fatalf("partial injection status=%s code=%s err=%v", status, code, err)
		}
		copied := filepath.Join(watch, filepath.Base(torrentPath))
		if attempt == 0 {
			if err := os.Remove(copied); err != nil {
				t.Fatalf("first client did not dispatch: %v", err)
			}
		} else if _, err := os.Stat(copied); !os.IsNotExist(err) {
			t.Fatalf("uncertain partial injection repeated the first client: %v", err)
		}
	}
}
