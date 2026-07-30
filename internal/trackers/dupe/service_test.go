// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type bannedGroupDefinition struct{}

func (bannedGroupDefinition) Name() string { return "DP" }

func (bannedGroupDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (bannedGroupDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeNone
}

func (bannedGroupDefinition) BannedGroups() []string { return []string{"SUBSPLEASE"} }

func (bannedGroupDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func testService(adapters map[string]Adapter) *Service {
	return &Service{
		cfg:      adaptersConfig(adapters),
		logger:   api.NopLogger{},
		adapters: adapters,
		filter: func(entries []api.DupeEntry, _ api.DuplicateSubject, _ string, _ config.Config, _ api.Logger) ([]api.DupeEntry, api.DupeMatch) {
			return cloneEntries(entries), api.DupeMatch{}
		},
		cancelWarningThreshold: time.Second,
	}
}

func TestCheckWithAssessmentReportsDebugBannedGroupAsBypassed(t *testing.T) {
	registry := trackerspkg.NewRegistry()
	if err := registry.Register(bannedGroupDefinition{}); err != nil {
		t.Fatalf("register banned-group definition: %v", err)
	}
	tempDir := t.TempDir()
	var adapterCalled atomic.Bool
	service := testService(map[string]Adapter{
		"DP": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			adapterCalled.Store(true)
			return Resolved(nil, nil)
		}),
	})
	service.banned = trackerspkg.NewBannedGroupCheckerWithRegistry(filepath.Join(tempDir, "upbrr.db"), registry)
	var (
		progressMu sync.Mutex
		progress   []api.DupeProgressUpdate
	)
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		progressMu.Lock()
		progress = append(progress, update)
		progressMu.Unlock()
	})
	summary, assessment, err := service.CheckWithAssessment(ctx, api.DuplicateSubject{
		SourcePath: filepath.Join(tempDir, "Example.Release.2026.1080p-GRP.mkv"),
		Tag:        "SubsPlease",
	}, []string{"DP"}, CheckOptions{BypassBannedGroups: true})
	if err != nil {
		t.Fatalf("check bypassed banned group: %v", err)
	}
	if adapterCalled.Load() {
		t.Fatal("bypassed banned-group result invoked duplicate adapter")
	}
	if len(summary.Results) != 1 || summary.Results[0].Status != "bypassed" ||
		summary.Results[0].SkipCode != NotRunBannedGroup ||
		!strings.Contains(summary.Results[0].SkipReason, "debug mode bypassed policy") {
		t.Fatalf("bypassed duplicate result = %#v", summary.Results)
	}
	decision, ok := assessment.Decision("DP")
	if !ok || decision.Verdict != VerdictWaived || decision.Authorization != AuthorizationWaiver {
		t.Fatalf("bypassed assessment decision = %#v found=%t", decision, ok)
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	if len(progress) == 0 || progress[len(progress)-1].Status != "bypassed" ||
		!strings.Contains(progress[len(progress)-1].Message, "debug mode bypassed policy") {
		t.Fatalf("bypassed duplicate progress = %#v", progress)
	}
}

func adaptersConfig(adapters map[string]Adapter) config.Config {
	trackers := make(map[string]config.TrackerConfig, len(adapters))
	for name := range adapters {
		trackers[name] = config.TrackerConfig{}
	}
	return config.Config{Trackers: config.TrackersConfig{Trackers: trackers}}
}

func TestAdapterResultDefensiveCopies(t *testing.T) {
	entries := []api.DupeEntry{{Name: "Example.Release.2026.1080p-GRP", Files: []string{"one.mkv"}}}
	notes := []string{"display only"}
	result := Resolved(entries, notes)
	entries[0].Name = "mutated"
	entries[0].Files[0] = "mutated"
	notes[0] = "mutated"

	gotEntries := result.Entries()
	gotNotes := result.Notes()
	if gotEntries[0].Name != "Example.Release.2026.1080p-GRP" || gotEntries[0].Files[0] != "one.mkv" || gotNotes[0] != "display only" {
		t.Fatalf("result changed through caller mutation: %#v %#v", gotEntries, gotNotes)
	}
	gotEntries[0].Files[0] = "again"
	if result.Entries()[0].Files[0] != "one.mkv" {
		t.Fatal("result accessor exposed mutable state")
	}
}

func TestCheckProjectionSetUsesExactCriteriaAndProjectsUploadName(t *testing.T) {
	t.Parallel()

	var received api.DuplicateSubject
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(_ context.Context, subject api.DuplicateSubject) AdapterResult {
			received = subject
			return Resolved(nil, nil)
		}),
	})
	projectionFingerprint := mustDupeFingerprint(t, "projection")
	criteriaFingerprint := mustDupeFingerprint(t, "criteria")
	inputFingerprint := mustDupeFingerprint(t, "input")
	catalogFingerprint := mustDupeFingerprint(t, "catalog")
	configFingerprint := mustDupeFingerprint(t, "config")
	policyFingerprint := mustDupeFingerprint(t, "policy")
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	projectionSet := api.TrackerReleaseProjectionSet{
		ID:         "projection-set-1",
		WorkflowID: "workflow-1",
		Revision:   1,
		Release: api.ReleaseSnapshotRef{
			ID:       "release-1",
			Revision: 1,
		},
		ReleaseRef: api.ReleaseRef{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
			Generation: 1,
		},
		Catalog: api.TrackerCatalogSnapshotRef{
			ID:       "catalog-1",
			Revision: 1,
		},
		Runtime: api.TrackerRuntimeSnapshotRef{
			ID:       "runtime-1",
			Revision: 1,
		},
		Selection: api.TrackerSelectionRef{
			ID:       "selection-1",
			Revision: 1,
		},
		InputFingerprint:  inputFingerprint,
		PolicyFingerprint: policyFingerprint,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:            "A",
			DisplayName:          "Tracker A",
			CanonicalReleaseName: "Example.Release.2026.1080p-GRP",
			UploadReleaseName:    "Example.Release.2026.TrackerA-GRP",
			AdditionalNames: []api.TrackerReleaseName{{
				Role:  api.TrackerReleaseNameRoleSearch,
				Value: "Example Release 2026",
			}},
			DuplicateCriteria: api.TrackerDuplicateCriteria{
				Name:   "Example Release 2026",
				Season: 2,
			},
			InputFingerprint:     inputFingerprint,
			CatalogFingerprint:   catalogFingerprint,
			ConfigFingerprint:    configFingerprint,
			ProjectorFingerprint: projectionFingerprint,
			CriteriaFingerprint:  criteriaFingerprint,
			Readiness:            api.ReadinessStatusReady,
			DupeReady:            true,
			UploadReady:          true,
		}},
		Status:    api.StageStatusReady,
		CreatedAt: now,
	}
	summary, _, err := service.CheckProjectionSet(context.Background(), api.DuplicateSubject{
		SourcePath:  projectionSet.ReleaseRef.SourcePath,
		ReleaseName: projectionSet.Projections[0].CanonicalReleaseName,
	}, projectionSet, CheckOptions{})
	if err != nil {
		t.Fatalf("check projection set: %v", err)
	}
	if received.Projection == nil || received.ReleaseName != "Example Release 2026" || received.SeasonInt != 2 {
		t.Fatalf("adapter subject = %#v", received)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("duplicate results = %#v", summary.Results)
	}
	result := summary.Results[0]
	if result.CanonicalReleaseName != projectionSet.Projections[0].CanonicalReleaseName ||
		result.UploadReleaseName != projectionSet.Projections[0].UploadReleaseName ||
		result.ProjectionFingerprint != projectionFingerprint || result.CriteriaFingerprint != criteriaFingerprint ||
		result.ProjectionStatus != api.ReadinessStatusReady {
		t.Fatalf("projected duplicate result = %#v", result)
	}
}

func mustDupeFingerprint(t *testing.T, value string) api.WorkflowFingerprint {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint %q: %v", value, err)
	}
	return fingerprint
}

func TestCheckReturnsResolvedOrderAndActualCompletionProgress(t *testing.T) {
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(startedA)
			<-releaseA
			return Resolved(nil, nil)
		}),
		"B": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(startedB)
			<-releaseB
			return Resolved(nil, nil)
		}),
	})

	var mu sync.Mutex
	completed := make([]string, 0, 2)
	completedB := make(chan struct{})
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		if update.Status == "completed" {
			mu.Lock()
			completed = append(completed, update.Tracker)
			mu.Unlock()
			if update.Tracker == "B" {
				close(completedB)
			}
		}
	})
	done := make(chan api.DupeCheckSummary, 1)
	go func() {
		summary, _ := service.Check(ctx, api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A", "B"})
		done <- summary
	}()
	waitForSignal := func(label string, signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for %s", label)
		}
	}
	waitForSignal("adapter A to start", startedA)
	waitForSignal("adapter B to start", startedB)
	close(releaseB)
	waitForSignal("adapter B completion progress", completedB)
	close(releaseA)
	var summary api.DupeCheckSummary
	select {
	case summary = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for duplicate check summary")
	}
	if len(summary.Results) != 2 || summary.Results[0].Tracker != "A" || summary.Results[1].Tracker != "B" {
		t.Fatalf("resolved order not preserved: %#v", summary.Results)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(completed) != 2 || completed[0] != "B" || completed[1] != "A" {
		t.Fatalf("completion progress not actual order: %v", completed)
	}
}

func TestCheckLimitsConcurrencyToFour(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	adapters := make(map[string]Adapter)
	for _, name := range []string{"A", "B", "C", "D", "E", "F"} {
		adapters[name] = AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			current := active.Add(1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			<-release
			active.Add(-1)
			return Resolved(nil, nil)
		})
	}
	done := make(chan struct{})
	go func() {
		_, _ = testService(adapters).Check(context.Background(), api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A", "B", "C", "D", "E", "F"})
		close(done)
	}()
	deadline := time.After(10 * time.Second)
	for maximum.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("workers did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if maximum.Load() > maxDupeWorkers {
		t.Fatalf("maximum concurrency = %d", maximum.Load())
	}
	close(release)
	<-done
}

func TestCheckCancellationWaitsForStartedAdapterAndReturnsCompletedEvidence(t *testing.T) {
	started := make(chan struct{})
	completed := make(chan struct{})
	release := make(chan struct{})
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(ctx context.Context, _ api.DuplicateSubject) AdapterResult {
			close(started)
			<-ctx.Done()
			<-release
			return Failed(FailureRequest, "canceled", ctx.Err())
		}),
		"B": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(completed)
			return Resolved(nil, nil)
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		summary api.DupeCheckSummary
		err     error
	}, 1)
	go func() {
		summary, err := service.Check(ctx, api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"B", "A"})
		done <- struct {
			summary api.DupeCheckSummary
			err     error
		}{summary: summary, err: err}
	}()
	<-started
	<-completed
	cancel()
	select {
	case <-done:
		t.Fatal("cancellation detached a started adapter")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	got := <-done
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v", got.err)
	}
	if len(got.summary.Results) != 1 || got.summary.Results[0].Tracker != "B" {
		t.Fatalf("completed evidence lost: %#v", got.summary.Results)
	}
}

func TestPublicProjectionBlanksPrivateDownloadsAndSanitizesURLQueries(t *testing.T) {
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			return Resolved([]api.DupeEntry{{
				Name:     "Example.Release.2026.1080p-GRP",
				Link:     "https://user@tracker.example/torrents.php?id=44&torrentid=55&token=secret#private",
				Download: "https://tracker.example/download/1?passkey=secret",
			}}, nil)
		}),
	})
	summary, err := service.Check(context.Background(), api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	entry := summary.Results[0].Raw[0]
	if entry.Download != "" || entry.Link != "https://tracker.example/torrents.php?id=44&torrentid=55" {
		t.Fatalf("private URL leaked: %#v", entry)
	}
}
