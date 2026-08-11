// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type barrierPlanDefinition struct {
	name        string
	started     chan<- string
	releasePrep <-chan struct{}
	submitted   chan<- string
}

func TestEmitTrackerPlanProgressUsesPerTrackerTaskCounters(t *testing.T) {
	t.Parallel()

	updates := make([]api.UploadProgressUpdate, 0, 2)
	ctx := api.WithUploadProgressReporter(context.Background(), func(update api.UploadProgressUpdate) {
		updates = append(updates, update)
	})

	emitTrackerPlanProgress(ctx, "Example.Release.2026-GRP", "ALPHA", "tracker_preparation", "running", "Preparing tracker plan")
	emitTrackerPlanProgress(ctx, "Example.Release.2026-GRP", "ALPHA", "tracker_preparation", "completed", "Tracker plan ready")

	if len(updates) != 2 {
		t.Fatalf("progress updates = %#v", updates)
	}
	if updates[0].CompletedPieces != 0 || updates[0].TotalPieces != 1 || updates[0].Percent != 0 {
		t.Fatalf("running progress = %#v", updates[0])
	}
	if updates[1].CompletedPieces != 1 || updates[1].TotalPieces != 1 || updates[1].Percent != 100 {
		t.Fatalf("terminal progress = %#v", updates[1])
	}
}

func (d barrierPlanDefinition) Name() string { return d.name }

func (barrierPlanDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func TestOrderedReadyTrackerPlanIndexesQueuesRehashLast(t *testing.T) {
	t.Parallel()

	plan := func(tracker string) TrackerPlan {
		return NewUploadPlan(tracker, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
			return api.UploadSummary{}, nil
		}, nil)
	}
	slots := []trackerPlanSlot{
		{tracker: "PTP", plan: plan("PTP")},
		{tracker: "HDB", plan: plan("HDB")},
		{tracker: "BTN", failure: &TrackerFailure{Tracker: "BTN"}},
		{tracker: "ANT", plan: plan("ANT")},
	}
	ready, delayedStart := orderedReadyTrackerPlanIndexes(slots, []string{"ptp", "ANT"})
	if delayedStart != 1 || !slices.Equal(ready, []int{1, 0, 3}) {
		t.Fatalf("ready indexes = %v at %d", ready, delayedStart)
	}
}

func TestTrackerPlanFailuresOmitsIntentionalSkips(t *testing.T) {
	t.Parallel()

	failures := trackerPlanFailures([]trackerPlanSlot{
		{tracker: "ALPHA", failure: &TrackerFailure{Tracker: "ALPHA", Code: PreparationFailureCodeSkipped}},
		{tracker: "BETA", failure: &TrackerFailure{Tracker: "BETA", Code: "prepare"}},
	})
	if len(failures) != 1 || failures[0].Tracker != "BETA" {
		t.Fatalf("tracker plan failures = %#v", failures)
	}
}

func TestSubmitTrackerPlansWaitsBetweenReusableAndRehashedBatches(t *testing.T) {
	var reusableFinished time.Time
	var rehashedStarted time.Time
	plan := func(tracker string, submit func()) TrackerPlan {
		return NewUploadPlan(tracker, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
			submit()
			return api.UploadSummary{Uploaded: 1}, nil
		}, nil)
	}
	slots := []trackerPlanSlot{
		{tracker: "HDB", plan: plan("HDB", func() { reusableFinished = time.Now() })},
		{tracker: "PTP", plan: plan("PTP", func() { rehashedStarted = time.Now() })},
	}
	svc := NewServiceWithRegistry(config.Config{
		TorrentCreation: config.TorrentCreationConfig{RehashCooldown: 1},
		PostUpload:      config.PostUploadConfig{MaxConcurrentTrackers: 2},
	}, api.NopLogger{}, nil, nil)
	svc.submitTrackerPlans(context.Background(), api.UploadSubject{
		SourcePath:       filepath.Join(t.TempDir(), "Example.Release.2026"),
		RehashedTrackers: []string{"PTP"},
	}, slots)
	if reusableFinished.IsZero() || rehashedStarted.IsZero() {
		t.Fatal("expected both upload batches to run")
	}
	if delay := rehashedStarted.Sub(reusableFinished); delay < 900*time.Millisecond {
		t.Fatalf("rehash upload started after %s, want at least 900ms", delay)
	}
}

func (barrierPlanDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (d barrierPlanDefinition) Prepare(ctx context.Context, _ PreparationInput) (TrackerPlan, *PreparationFailure) {
	d.started <- d.name
	select {
	case <-d.releasePrep:
	case <-ctx.Done():
		return TrackerPlan{}, NewPreparationFailure(d.name, "canceled", "preparation canceled", ctx.Err())
	}
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{Tracker: d.name, Status: "ready"}, func(context.Context) (api.UploadSummary, error) {
		d.submitted <- d.name
		return api.UploadSummary{Uploaded: 1}, nil
	}, nil), nil
}

func TestUploadPreparationHasBoundedPoolAndFullBarrier(t *testing.T) {
	t.Parallel()
	names := []string{"AITHER", "BHD", "BLU", "HDB", "BTN", "PTP"}
	started := make(chan string, len(names))
	submitted := make(chan string, len(names))
	releasePrep := make(chan struct{})
	registry := NewRegistry()
	for _, name := range names {
		if err := registry.Register(barrierPlanDefinition{
			name:        name,
			started:     started,
			releasePrep: releasePrep,
			submitted:   submitted,
		}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList(names)}}, nil, nil, registry)
	done := make(chan error, 1)
	go func() {
		summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "Example.Release.2026"})
		if err == nil && summary.Uploaded != len(names) {
			err = fmt.Errorf("uploaded=%d", summary.Uploaded)
		}
		done <- err
	}()

	for range defaultMaxConcurrentTrackerPreparations {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("preparation pool did not fill")
		}
	}
	select {
	case tracker := <-started:
		t.Fatalf("preparation exceeded bound: %s", tracker)
	case tracker := <-submitted:
		t.Fatalf("submission crossed preparation barrier: %s", tracker)
	default:
	}
	close(releasePrep)
	for range names {
		select {
		case <-submitted:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent submission did not finish")
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("upload: %v", err)
	}
}

type readyReleasePlanDefinition struct {
	name      string
	releases  *atomic.Int32
	submitted *atomic.Int32
	prepared  chan<- struct{}
}

func (d readyReleasePlanDefinition) Name() string { return d.name }

func (readyReleasePlanDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (readyReleasePlanDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (d readyReleasePlanDefinition) Prepare(context.Context, PreparationInput) (TrackerPlan, *PreparationFailure) {
	if d.prepared != nil {
		d.prepared <- struct{}{}
	}
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
		d.submitted.Add(1)
		return api.UploadSummary{Uploaded: 1}, nil
	}, func() error {
		d.releases.Add(1)
		return nil
	}), nil
}

type canceledPreparationDefinition struct {
	name    string
	started chan<- struct{}
	wait    <-chan struct{}
}

type retainedProjectionDefinition struct {
	name           string
	prepared       *atomic.Int32
	submitted      *atomic.Int32
	input          chan<- PreparationInput
	artifactPolicy *UploadArtifactPolicy
}

type planInfoLogger struct {
	api.NopLogger
	mu    sync.Mutex
	infos []string
}

func (l *planInfoLogger) Infof(format string, args ...any) {
	l.mu.Lock()
	l.infos = append(l.infos, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *planInfoLogger) containsInfo(value string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.infos, value)
}

func (d retainedProjectionDefinition) Name() string { return d.name }

func (retainedProjectionDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeNone
}

func (retainedProjectionDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (d retainedProjectionDefinition) UploadArtifactPolicy() *UploadArtifactPolicy {
	return d.artifactPolicy
}

func (d retainedProjectionDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	d.prepared.Add(1)
	return prepareTestDefinition(ctx, input, d)
}

func (d retainedProjectionDefinition) prepareDryRun(_ context.Context, input PreparationInput) (api.TrackerDryRunEntry, error) {
	releaseName, err := input.ReviewedUploadName()
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:           d.name,
		Status:            "ready",
		UploadReleaseName: releaseName,
		ReleaseName:       releaseName,
		Payload:           map[string]string{"name": releaseName, "api_token": "private"},
	}, nil
}

//nolint:unparam // Error is required by the tracker submission callback contract.
func (d retainedProjectionDefinition) submit(_ context.Context, input PreparationInput) (api.UploadSummary, error) {
	d.submitted.Add(1)
	d.input <- input
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:    d.name,
			TorrentID:  "123",
			TorrentURL: "https://tracker.example.invalid/torrents/123",
		}},
	}, nil
}

func TestRetainedUploadPlanExecutesExactReviewedProjectionWithoutRepreparing(t *testing.T) {
	t.Parallel()

	var prepared atomic.Int32
	var submitted atomic.Int32
	inputs := make(chan PreparationInput, 1)
	registry := NewRegistry()
	if err := registry.Register(retainedProjectionDefinition{
		name:      "EXAMPLE",
		prepared:  &prepared,
		submitted: &submitted,
		input:     inputs,
	}); err != nil {
		t.Fatalf("register retained definition: %v", err)
	}
	service := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	projection := api.TrackerReleaseProjection{
		TrackerID:         "EXAMPLE",
		UploadReleaseName: "Example.Release.2026.REVIEWED-GRP",
		Readiness:         api.ReadinessStatusReady,
		UploadReady:       true,
	}
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026.ORIGINAL-GRP"},
		[]api.TrackerReleaseProjection{projection},
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	preparations := retained.Preparations()
	if prepared.Load() != 1 || submitted.Load() != 0 || len(preparations) != 1 ||
		preparations[0].Preview.UploadReleaseName != projection.UploadReleaseName {
		t.Fatalf("retained preparation = %#v prepared=%d submitted=%d", preparations, prepared.Load(), submitted.Load())
	}
	results, err := retained.Execute(context.Background())
	if err != nil {
		t.Fatalf("execute retained upload plan: %v", err)
	}
	if prepared.Load() != 1 || submitted.Load() != 1 || len(results) != 1 || results[0].Summary.Uploaded != 1 {
		t.Fatalf("retained execution = %#v prepared=%d submitted=%d", results, prepared.Load(), submitted.Load())
	}
	input := <-inputs
	if input.Projection == nil || input.Meta.ReleaseName != "Example.Release.2026.ORIGINAL-GRP" {
		t.Fatalf("executed input = %#v", input)
	}
	if _, err := retained.Execute(context.Background()); !errors.Is(err, ErrPlanReleased) && !errors.Is(err, ErrPlanAlreadyUsed) {
		t.Fatalf("second retained execution error = %v", err)
	}
}

func TestRetainedUploadPlanLogsReturnedTorrentPageURL(t *testing.T) {
	t.Parallel()

	var prepared atomic.Int32
	var submitted atomic.Int32
	logger := &planInfoLogger{}
	registry := NewRegistry()
	if err := registry.Register(retainedProjectionDefinition{
		name:      "EXAMPLE",
		prepared:  &prepared,
		submitted: &submitted,
		input:     make(chan PreparationInput, 1),
	}); err != nil {
		t.Fatalf("register retained definition: %v", err)
	}
	service := NewServiceWithRegistry(config.Config{}, logger, nil, registry)
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{SourcePath: "Example.Release.2026"},
		[]api.TrackerReleaseProjection{{
			TrackerID:         "EXAMPLE",
			UploadReleaseName: "Example.Release.2026.1080p-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		}},
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	if _, err := retained.Execute(context.Background()); err != nil {
		t.Fatalf("execute retained upload plan: %v", err)
	}
	if !logger.containsInfo("trackers: EXAMPLE torrent URL: https://tracker.example.invalid/torrents/123") {
		t.Fatal("retained upload did not log returned torrent page URL")
	}
}

func TestSanitizeTorrentPageURLForLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "group and torrent identifiers",
			value: "https://user@tracker.example.invalid/torrents.php?id=44&torrentid=55&token=private#fragment",
			want:  "https://tracker.example.invalid/torrents.php?id=44&torrentid=55",
		},
		{
			name:  "torrent details page selector",
			value: "https://tracker.example.invalid/index.php?page=torrent-details&id=77",
			want:  "https://tracker.example.invalid/index.php?id=77&page=torrent-details",
		},
		{
			name:  "torrent hash identifier",
			value: "https://tracker.example.invalid/details.php?hash=0123456789abcdef0123456789abcdef01234567",
			want:  "https://tracker.example.invalid/details.php?hash=0123456789abcdef0123456789abcdef01234567",
		},
		{
			name:  "upload redirect identifier",
			value: "https://tracker.example.invalid/details.php?uploaded=1&id=88",
			want:  "https://tracker.example.invalid/details.php?id=88&uploaded=1",
		},
		{
			name:  "unsupported scheme",
			value: "ftp://tracker.example.invalid/torrents/123",
			want:  "",
		},
		{
			name:  "relative URL",
			value: "/torrents/123",
			want:  "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if sanitizeTorrentPageURLForLog(test.value) != test.want {
				t.Fatal("unexpected sanitized torrent page URL")
			}
		})
	}
}

func TestRetainedUploadPlanExecutesOnlyApprovedTrackers(t *testing.T) {
	t.Parallel()

	var alphaPrepared atomic.Int32
	var alphaSubmitted atomic.Int32
	var betaPrepared atomic.Int32
	var betaSubmitted atomic.Int32
	registry := NewRegistry()
	for _, definition := range []Definition{
		retainedProjectionDefinition{
			name:      "ALPHA",
			prepared:  &alphaPrepared,
			submitted: &alphaSubmitted,
			input:     make(chan PreparationInput, 1),
		},
		retainedProjectionDefinition{
			name:      "BETA",
			prepared:  &betaPrepared,
			submitted: &betaSubmitted,
			input:     make(chan PreparationInput, 1),
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register retained definition: %v", err)
		}
	}
	service := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026-GRP"},
		[]api.TrackerReleaseProjection{
			{
				TrackerID:         "ALPHA",
				UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
			{
				TrackerID:         "BETA",
				UploadReleaseName: "Example.Release.2026.BETA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	results, err := retained.ExecuteSelected(context.Background(), []string{"BETA"})
	if err != nil {
		t.Fatalf("execute selected retained upload plan: %v", err)
	}
	if alphaPrepared.Load() != 1 || betaPrepared.Load() != 1 ||
		alphaSubmitted.Load() != 0 || betaSubmitted.Load() != 1 ||
		len(results) != 1 || results[0].Tracker != "BETA" {
		t.Fatalf(
			"selected retained execution = %#v prepared=%d/%d submitted=%d/%d",
			results,
			alphaPrepared.Load(),
			betaPrepared.Load(),
			alphaSubmitted.Load(),
			betaSubmitted.Load(),
		)
	}
}

func TestRetainedUploadPlanRejectsStaleReleaseNamePolicyTrackerLocally(t *testing.T) {
	t.Parallel()

	var prepared atomic.Int32
	registry := NewRegistry()
	if err := registry.Register(retainedProjectionDefinition{
		name:      "EXAMPLE",
		prepared:  &prepared,
		submitted: &atomic.Int32{},
		input:     make(chan PreparationInput, 1),
	}); err != nil {
		t.Fatalf("register retained definition: %v", err)
	}
	service := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026-GRP"},
		[]api.TrackerReleaseProjection{{
			TrackerID:            "EXAMPLE",
			UploadReleaseName:    "Example.Release.2026-GRP",
			Readiness:            api.ReadinessStatusReady,
			UploadReady:          true,
			ProjectorFingerprint: "projection-v1",
			PolicyDecisions: []api.TrackerPolicyDecision{
				{Code: releaseNamePolicyDecisionCode, Decision: "standalone/example/v0"},
				{Code: releaseNameInstructionCode, Decision: "automatic"},
			},
		}},
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	preparations := retained.Preparations()
	if len(preparations) != 1 || preparations[0].Failure == nil || preparations[0].Failure.Code != "name_policy_stale" {
		t.Fatalf("retained preparation = %#v", preparations)
	}
	if prepared.Load() != 0 {
		t.Fatalf("tracker prepared after stale naming policy: %d", prepared.Load())
	}
}

func TestRetainedUploadPlanPublishesExactDistinctTrackerArtifacts(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Example.Release.2026.mkv")
	baseTorrentPath := filepath.Join(tmp, "release.torrent")
	createServiceTestTorrent(t, sourcePath, baseTorrentPath)
	registry := NewRegistry()
	var prepared atomic.Int32
	var submitted atomic.Int32
	for _, definition := range []Definition{
		retainedProjectionDefinition{
			name:           "ALPHA",
			prepared:       &prepared,
			submitted:      &submitted,
			artifactPolicy: &UploadArtifactPolicy{Source: "AlphaSource"},
		},
		retainedProjectionDefinition{
			name:           "BETA",
			prepared:       &prepared,
			submitted:      &submitted,
			artifactPolicy: &UploadArtifactPolicy{Source: "BetaSource"},
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register retained definition: %v", err)
		}
	}
	service := NewServiceWithRegistry(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "upbrr.db")}}, nil, nil, registry)
	projections := []api.TrackerReleaseProjection{
		{
			TrackerID:         "ALPHA",
			UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		},
		{
			TrackerID:         "BETA",
			UploadReleaseName: "Example.Release.2026.BETA-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		},
	}
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{
			SourcePath:  sourcePath,
			TorrentPath: baseTorrentPath,
			ReleaseName: "Example.Release.2026-GRP",
		},
		projections,
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	defer func() { _ = retained.Release() }()
	preparations := retained.Preparations()
	if len(preparations) != 2 {
		t.Fatalf("retained preparations = %d", len(preparations))
	}
	if preparations[0].TorrentPath == "" || preparations[1].TorrentPath == "" || preparations[0].TorrentPath == preparations[1].TorrentPath {
		t.Fatalf("retained tracker artifact paths = %q, %q", preparations[0].TorrentPath, preparations[1].TorrentPath)
	}
	assertTrackerArtifact(t, preparations[0].TorrentPath, "", "AlphaSource")
	assertTrackerArtifact(t, preparations[1].TorrentPath, "", "BetaSource")
	if prepared.Load() != 2 || submitted.Load() != 0 {
		t.Fatalf("prepared=%d submitted=%d", prepared.Load(), submitted.Load())
	}
}

func TestRetainedUploadPlanKeepsArtifactFailureTrackerLocal(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Example.Release.2026.mkv")
	baseTorrentPath := filepath.Join(tmp, "release.torrent")
	createServiceTestTorrent(t, sourcePath, baseTorrentPath)
	registry := NewRegistry()
	var prepared atomic.Int32
	var submitted atomic.Int32
	for _, definition := range []Definition{
		retainedProjectionDefinition{
			name:           "ALPHA",
			prepared:       &prepared,
			submitted:      &submitted,
			artifactPolicy: &UploadArtifactPolicy{Source: "AlphaSource", RequireAnnounce: true},
		},
		retainedProjectionDefinition{
			name:           "BETA",
			prepared:       &prepared,
			submitted:      &submitted,
			artifactPolicy: &UploadArtifactPolicy{Source: "BetaSource"},
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register retained definition: %v", err)
		}
	}
	service := NewServiceWithRegistry(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "upbrr.db")}}, nil, nil, registry)
	retained, err := service.PrepareRetainedUploadPlan(
		context.Background(),
		api.UploadSubject{
			SourcePath:  sourcePath,
			TorrentPath: baseTorrentPath,
			ReleaseName: "Example.Release.2026-GRP",
		},
		[]api.TrackerReleaseProjection{
			{
				TrackerID:         "ALPHA",
				UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
			{
				TrackerID:         "BETA",
				UploadReleaseName: "Example.Release.2026.BETA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare retained upload plan: %v", err)
	}
	defer func() { _ = retained.Release() }()
	preparations := retained.Preparations()
	if len(preparations) != 2 || preparations[0].Failure == nil ||
		!strings.Contains(preparations[0].Failure.Message, "required announce URL is missing") {
		t.Fatalf("required-announce preparation = %#v", preparations)
	}
	if preparations[1].Failure != nil || preparations[1].TorrentPath == "" {
		t.Fatalf("unrelated tracker preparation = %#v", preparations[1])
	}
	assertTrackerArtifact(t, preparations[1].TorrentPath, "", "BetaSource")
}

func (d canceledPreparationDefinition) Name() string { return d.name }

func (canceledPreparationDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (canceledPreparationDefinition) DefaultBaseURL() string {
	return "https://tracker.example.invalid"
}

func (d canceledPreparationDefinition) Prepare(ctx context.Context, _ PreparationInput) (TrackerPlan, *PreparationFailure) {
	if d.wait != nil {
		<-d.wait
	}
	d.started <- struct{}{}
	<-ctx.Done()
	return TrackerPlan{}, NewPreparationFailure(d.name, "canceled", "preparation canceled", ctx.Err())
}

func TestUploadCancellationBeforeBarrierSubmitsNoneAndReleasesReadyPlans(t *testing.T) {
	t.Parallel()
	var releases atomic.Int32
	var submits atomic.Int32
	started := make(chan struct{}, 1)
	ready := make(chan struct{}, 1)
	registry := NewRegistry()
	if err := registry.Register(readyReleasePlanDefinition{
		name:      "AITHER",
		releases:  &releases,
		submitted: &submits,
		prepared:  ready,
	}); err != nil {
		t.Fatalf("register ready: %v", err)
	}
	if err := registry.Register(canceledPreparationDefinition{
		name:    "BLU",
		started: started,
		wait:    ready,
	}); err != nil {
		t.Fatalf("register blocking: %v", err)
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}}}, nil, nil, registry)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "Example.Release.2026"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("blocking preparation did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("upload error = %v", err)
	}
	if submits.Load() != 0 || releases.Load() != 1 {
		t.Fatalf("submits=%d releases=%d", submits.Load(), releases.Load())
	}
}

type warningLogger struct {
	api.NopLogger
	mu       sync.Mutex
	warnings []string
}

func (l *warningLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func TestUploadReleaseFailureAfterRemoteSuccessIsSanitizedWarningOnly(t *testing.T) {
	t.Parallel()
	logger := &warningLogger{}
	registry := NewRegistry()
	definition := readyReleasePlanDefinition{
		name:      "AITHER",
		releases:  &atomic.Int32{},
		submitted: &atomic.Int32{},
	}
	if err := registry.Register(releaseErrorDefinition{readyReleasePlanDefinition: definition}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}, logger, nil, registry)
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "Example.Release.2026"})
	if err != nil || summary.Uploaded != 1 {
		t.Fatalf("upload = %#v, %v", summary, err)
	}
	logger.mu.Lock()
	joined := strings.Join(logger.warnings, "\n")
	logger.mu.Unlock()
	if !strings.Contains(joined, "plan release failed") || strings.Contains(joined, "secret-value") {
		t.Fatalf("warnings = %q", joined)
	}
}

type releaseErrorDefinition struct{ readyReleasePlanDefinition }

func (d releaseErrorDefinition) Prepare(context.Context, PreparationInput) (TrackerPlan, *PreparationFailure) {
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
		d.submitted.Add(1)
		return api.UploadSummary{Uploaded: 1}, nil
	}, func() error {
		d.releases.Add(1)
		return errors.New("token=secret-value cleanup failed")
	}), nil
}
