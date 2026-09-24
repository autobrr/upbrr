// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type activationTestInstaller struct {
	mu          sync.Mutex
	rows        *[]string
	retired     RetiredRuntime
	generations []RuntimeGeneration
}

func (i *activationTestInstaller) Install(generation RuntimeGeneration) RetiredRuntime {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.rows != nil {
		*i.rows = append(*i.rows, "install")
	}
	i.generations = append(i.generations, generation)
	return i.retired
}

type activationTestOwner struct {
	closed  atomic.Bool
	onClose func()
}

func (o *activationTestOwner) Close() error {
	o.closed.Store(true)
	if o.onClose != nil {
		o.onClose()
	}
	return nil
}

func TestRuntimeActivatorOwnsOrderedTransition(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "3")
	repo := openRuntimeActivationTestRepo(t)
	rows := make([]string, 0, 5)
	builtOwner := &activationTestOwner{onClose: func() { rows = append(rows, "built-close") }}
	retiredOwner := &activationTestOwner{onClose: func() { rows = append(rows, "retired-close") }}
	installer := &activationTestInstaller{
		rows:    &rows,
		retired: RetiredRuntime{Owner: retiredOwner},
	}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatalf("new activator: %v", err)
	}

	var stored config.Config
	activator.deps = runtimeActivationDeps{
		build: func(_ context.Context, cfg config.Config, gotRepo *db.SQLiteRepository) (RuntimeGeneration, error) {
			rows = append(rows, "build")
			if gotRepo != repo {
				t.Fatal("build received a different repository")
			}
			return RuntimeGeneration{Config: cfg, Owner: builtOwner}, nil
		},
		cookies: func(_ context.Context, gotRepo *db.SQLiteRepository, dbPath string, _ api.Logger) error {
			rows = append(rows, "cookies")
			if gotRepo != repo || dbPath != repo.DBPath() {
				t.Fatalf("cookie maintenance scope = (%p, %q), want (%p, %q)", gotRepo, dbPath, repo, repo.DBPath())
			}
			return nil
		},
		persist: func(_ context.Context, cfg *config.Config, gotRepo *db.SQLiteRepository, dbPath string, _ api.Logger) error {
			rows = append(rows, "persist")
			stored = *cfg
			if gotRepo != repo || dbPath != repo.DBPath() {
				t.Fatalf("persistence scope = (%p, %q), want (%p, %q)", gotRepo, dbPath, repo, repo.DBPath())
			}
			return nil
		},
	}

	candidate := validRuntimeActivationConfig()
	candidate.MainSettings.DBPath = filepath.Join(t.TempDir(), "ignored.db")
	if err := activator.Activate(context.Background(), candidate); err != nil {
		t.Fatalf("activate: %v", err)
	}

	wantRows := []string{"build", "cookies", "persist", "install", "retired-close"}
	if len(rows) != len(wantRows) {
		t.Fatalf("transition rows = %v, want %v", rows, wantRows)
	}
	for index := range wantRows {
		if rows[index] != wantRows[index] {
			t.Fatalf("transition rows = %v, want %v", rows, wantRows)
		}
	}
	if builtOwner.closed.Load() {
		t.Fatal("installed generation was retired")
	}
	if !retiredOwner.closed.Load() {
		t.Fatal("replaced generation was not retired")
	}
	if stored.MainSettings.DBPath != repo.DBPath() || stored.ScreenshotHandling.Screens != 1 {
		t.Fatalf("stored config = %#v", stored)
	}
	if len(installer.generations) != 1 {
		t.Fatalf("installed generations = %d, want 1", len(installer.generations))
	}
	installed := installer.generations[0].Config
	if installed.MainSettings.DBPath != repo.DBPath() || installed.ScreenshotHandling.Screens != 3 {
		t.Fatalf("installed runtime config = %#v", installed)
	}
	if candidate.MainSettings.DBPath == repo.DBPath() {
		t.Fatal("activation mutated the caller's config")
	}
}

func TestRuntimeActivatorFailureStagesDoNotInstall(t *testing.T) {
	tests := []struct {
		name      string
		stage     ActivationStage
		configure func(*RuntimeActivator, *activationTestOwner)
	}{
		{
			name:  "build",
			stage: ActivationStageBuild,
			configure: func(activator *RuntimeActivator, _ *activationTestOwner) {
				activator.deps.build = func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
					return RuntimeGeneration{}, errors.New("build failed")
				}
			},
		},
		{
			name:  "cookies",
			stage: ActivationStageCookies,
			configure: func(activator *RuntimeActivator, owner *activationTestOwner) {
				activator.deps.build = activationTestBuild(owner)
				activator.deps.cookies = func(context.Context, *db.SQLiteRepository, string, api.Logger) error {
					return errors.New("cookies failed")
				}
			},
		},
		{
			name:  "persist",
			stage: ActivationStagePersist,
			configure: func(activator *RuntimeActivator, owner *activationTestOwner) {
				activator.deps.build = activationTestBuild(owner)
				activator.deps.cookies = activationTestCookies
				activator.deps.persist = func(context.Context, *config.Config, *db.SQLiteRepository, string, api.Logger) error {
					return errors.New("persist failed")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := openRuntimeActivationTestRepo(t)
			installer := &activationTestInstaller{}
			activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
			if err != nil {
				t.Fatalf("new activator: %v", err)
			}
			owner := &activationTestOwner{}
			test.configure(activator, owner)
			activator.deps.persistActivated = nil
			activator.deps.loadActivation = nil

			err = activator.Activate(context.Background(), validRuntimeActivationConfig())
			assertActivationStage(t, err, test.stage)
			if len(installer.generations) != 0 {
				t.Fatal("failed activation installed a generation")
			}
			if test.stage != ActivationStageBuild && !owner.closed.Load() {
				t.Fatal("failed activation did not close its built generation")
			}
		})
	}
}

func TestRuntimeActivatorReportsStoredAndRuntimeValidationStages(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	installer := &activationTestInstaller{}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatalf("new activator: %v", err)
	}

	invalidStored := validRuntimeActivationConfig()
	invalidStored.ScreenshotHandling.Screens = 0
	err = activator.Activate(context.Background(), invalidStored)
	assertActivationStage(t, err, ActivationStageValidateStored)

	t.Setenv("UA_DEFAULT_SCREENS", "0")
	err = activator.Activate(context.Background(), validRuntimeActivationConfig())
	assertActivationStage(t, err, ActivationStageValidateRuntime)
	if len(installer.generations) != 0 {
		t.Fatal("invalid config installed a generation")
	}
}

func TestRuntimeActivatorSerializesCompleteTransition(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	installer := &activationTestInstaller{}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatalf("new activator: %v", err)
	}

	entered := make(chan int, 2)
	releaseFirst := make(chan struct{})
	var builds atomic.Int32
	activator.deps = runtimeActivationDeps{
		build: func(_ context.Context, cfg config.Config, _ *db.SQLiteRepository) (RuntimeGeneration, error) {
			build := int(builds.Add(1))
			entered <- build
			if build == 1 {
				<-releaseFirst
			}
			return RuntimeGeneration{Config: cfg}, nil
		},
		cookies: activationTestCookies,
		persist: func(context.Context, *config.Config, *db.SQLiteRepository, string, api.Logger) error { return nil },
	}

	errs := make(chan error, 2)
	go func() { errs <- activator.Activate(context.Background(), validRuntimeActivationConfig()) }()
	if build := <-entered; build != 1 {
		t.Fatalf("first build = %d", build)
	}
	go func() { errs <- activator.Activate(context.Background(), validRuntimeActivationConfig()) }()
	select {
	case build := <-entered:
		t.Fatalf("second activation entered build %d before first transition completed", build)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if build := <-entered; build != 2 {
		t.Fatalf("second build = %d", build)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("activate: %v", err)
		}
	}
}

func TestRuntimeActivatorPersistsStoredOnlyChangeWithoutBuilding(t *testing.T) {
	t.Setenv("UA_DEFAULT_TMDB_API", "env-value")
	repo := openRuntimeActivationTestRepo(t)
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), &activationTestInstaller{})
	if err != nil {
		t.Fatal(err)
	}
	stored := validRuntimeActivationConfig()
	stored.MainSettings.DBPath = repo.DBPath()
	candidate := stored
	candidate.MainSettings.TMDBAPI = "saved-value"
	var persisted config.Config
	activator.deps = runtimeActivationDeps{
		build: func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
			t.Fatal("stored-only activation rebuilt runtime")
			return RuntimeGeneration{}, nil
		},
		cookies: func(context.Context, *db.SQLiteRepository, string, api.Logger) error {
			t.Fatal("stored-only activation maintained cookies")
			return nil
		},
		persist: func(_ context.Context, cfg *config.Config, _ *db.SQLiteRepository, _ string, _ api.Logger) error {
			persisted = *cfg
			return nil
		},
		loadStored: func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return &stored, nil },
		loadActivation: func(context.Context, *db.SQLiteRepository) (api.ConfigActivation, error) {
			return api.ConfigActivation{
				Status:           api.ConfigActivationActive,
				ActiveGeneration: 4,
				Impacts:          []api.ConfigImpact{},
			}, nil
		},
	}

	result, err := activator.ActivateResult(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.MainSettings.TMDBAPI != "saved-value" {
		t.Fatal("persisted stored value did not match candidate")
	}
	if result.ActiveGeneration != 4 || result.Status != api.ConfigActivationActive {
		t.Fatalf("activation result = %#v", result)
	}
}

func TestRuntimeActivatorExactNoopDoesNotPersistOrBuild(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), &activationTestInstaller{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := validRuntimeActivationConfig()
	candidate.MainSettings.DBPath = repo.DBPath()
	stored, err := cloneConfig(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.MergeMissingTrackerDefaults(stored); err != nil {
		t.Fatal(err)
	}
	activator.deps = runtimeActivationDeps{
		build: func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
			t.Fatal("exact no-op rebuilt runtime")
			return RuntimeGeneration{}, nil
		},
		persist: func(context.Context, *config.Config, *db.SQLiteRepository, string, api.Logger) error {
			t.Fatal("exact no-op persisted config")
			return nil
		},
		loadStored: func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return stored, nil },
		loadActivation: func(context.Context, *db.SQLiteRepository) (api.ConfigActivation, error) {
			return api.ConfigActivation{
				Status:           api.ConfigActivationActive,
				ActiveGeneration: 2,
				Impacts:          []api.ConfigImpact{},
			}, nil
		},
	}
	result, err := activator.ActivateResult(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveGeneration != 2 {
		t.Fatalf("activation result = %#v", result)
	}
}

func TestRuntimeActivatorDefersEffectiveChangeWhileWorkflowIsActive(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), &activationTestInstaller{})
	if err != nil {
		t.Fatal(err)
	}
	stored := validRuntimeActivationConfig()
	stored.MainSettings.DBPath = repo.DBPath()
	candidate := stored
	candidate.Metadata.KeepImages = true
	var savedCandidate []byte
	activator.deps = runtimeActivationDeps{
		build: func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
			t.Fatal("pending activation built a runtime")
			return RuntimeGeneration{}, nil
		},
		loadStored:     func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return &stored, nil },
		activationSafe: func(context.Context, *db.SQLiteRepository) (bool, error) { return false, nil },
		loadActivation: func(context.Context, *db.SQLiteRepository) (api.ConfigActivation, error) {
			return api.ConfigActivation{
				Status:           api.ConfigActivationActive,
				ActiveGeneration: 8,
				Impacts:          []api.ConfigImpact{},
			}, nil
		},
		savePending: func(_ context.Context, _ *db.SQLiteRepository, ownerID string, candidate []byte, impacts []api.ConfigImpactDetail) (api.ConfigActivation, error) {
			if ownerID != "system" {
				t.Fatalf("pending owner = %q", ownerID)
			}
			savedCandidate = candidate
			if !slices.ContainsFunc(impacts, func(impact api.ConfigImpactDetail) bool { return impact.Kind == api.ConfigImpactProvider }) {
				t.Fatalf("impacts = %v, want provider", impacts)
			}
			return api.ConfigActivation{
				Status:            api.ConfigActivationPending,
				ActiveGeneration:  8,
				PendingGeneration: 9,
				Impacts:           []api.ConfigImpact{api.ConfigImpactProvider},
			}, nil
		},
	}
	result, err := activator.ActivateResult(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.ConfigActivationPending || len(savedCandidate) == 0 {
		t.Fatalf("pending result = %#v candidate=%d bytes", result, len(savedCandidate))
	}
}

func TestRuntimeActivatorMarksDeferredFailuresTerminalAndAcceptsCorrection(t *testing.T) {
	tests := []struct {
		name             string
		code             api.ConfigActivationFailureCode
		configureFailure func(*RuntimeActivator)
	}{
		{
			name: "build",
			code: api.ConfigActivationFailureBuild,
			configureFailure: func(activator *RuntimeActivator) {
				activator.deps.build = func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
					return RuntimeGeneration{}, errors.New("build failure")
				}
			},
		},
		{
			name: "cookies",
			code: api.ConfigActivationFailureCookies,
			configureFailure: func(activator *RuntimeActivator) {
				activator.deps.cookies = func(context.Context, *db.SQLiteRepository, string, api.Logger) error {
					return errors.New("cookie failure")
				}
			},
		},
		{
			name: "persist",
			code: api.ConfigActivationFailurePersist,
			configureFailure: func(activator *RuntimeActivator) {
				activator.deps.persistActivated = func(
					context.Context,
					*config.Config,
					*db.SQLiteRepository,
					string,
					api.Logger,
					api.ConfigActivation,
					api.WorkflowFingerprint,
					[]api.ConfigImpactDetail,
					db.ConfigActivationWorkflowTransform,
				) (api.ConfigActivation, error) {
					return api.ConfigActivation{}, errors.New("persistence failure")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := openRuntimeActivationTestRepo(t)
			activator, err := NewRuntimeActivator(repo, repo.DBPath(), &activationTestInstaller{})
			if err != nil {
				t.Fatal(err)
			}
			stored := validRuntimeActivationConfig()
			stored.MainSettings.DBPath = repo.DBPath()
			candidate := stored
			candidate.Metadata.KeepImages = true
			safe := false
			activator.deps.loadStored = func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return &stored, nil }
			activator.deps.loadActivation = func(ctx context.Context, repo *db.SQLiteRepository) (api.ConfigActivation, error) {
				return repo.LoadConfigActivation(ctx)
			}
			activator.deps.savePending = func(
				ctx context.Context,
				repo *db.SQLiteRepository,
				ownerID string,
				candidate []byte,
				impacts []api.ConfigImpactDetail,
			) (api.ConfigActivation, error) {
				return repo.SavePendingConfigActivation(ctx, ownerID, candidate, impacts)
			}
			activator.deps.activationSafe = func(context.Context, *db.SQLiteRepository) (bool, error) { return safe, nil }
			activator.deps.build = func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
				return RuntimeGeneration{Owner: &activationTestOwner{}}, nil
			}
			activator.deps.cookies = activationTestCookies
			activator.deps.persistActivated = activateRuntimeConfigForTest

			pending, err := activator.ActivateResult(t.Context(), candidate)
			if err != nil {
				t.Fatalf("defer candidate: %v", err)
			}
			if pending.Status != api.ConfigActivationPending {
				t.Fatalf("deferred activation = %#v", pending)
			}
			safe = true
			test.configureFailure(activator)
			failed, err := activator.ActivatePending(t.Context())
			if err != nil {
				t.Fatalf("activate deferred candidate: %v", err)
			}
			if failed.Status != api.ConfigActivationFailed || failed.ActivationID != pending.ActivationID || failed.FailureCode != test.code {
				t.Fatalf("failed activation = %#v", failed)
			}
			if _, _, err := repo.LoadPendingConfigActivationCandidate(t.Context()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("failed activation retained candidate: %v", err)
			}

			activator.deps.build = func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
				return RuntimeGeneration{Owner: &activationTestOwner{}}, nil
			}
			activator.deps.cookies = activationTestCookies
			activator.deps.persistActivated = activateRuntimeConfigForTest
			corrected := stored
			corrected.Metadata.OnlyID = true
			active, err := activator.ActivateResult(t.Context(), corrected)
			if err != nil {
				t.Fatalf("activate corrected candidate: %v", err)
			}
			if active.Status != api.ConfigActivationActive || active.ActiveGeneration != 1 {
				t.Fatalf("corrected activation = %#v", active)
			}
		})
	}
}

func TestRuntimeActivatorCompletesPendingCandidateAlreadyEffectiveAfterUpgrade(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	installer := &activationTestInstaller{}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatal(err)
	}
	stored := validRuntimeActivationConfig()
	stored.MainSettings.DBPath = repo.DBPath()
	activator.deps.loadStored = func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return &stored, nil }
	activator.deps.loadActivation = func(ctx context.Context, repo *db.SQLiteRepository) (api.ConfigActivation, error) {
		return repo.LoadConfigActivation(ctx)
	}
	activator.deps.savePending = func(ctx context.Context, repo *db.SQLiteRepository, ownerID string, candidate []byte, impacts []api.ConfigImpactDetail) (api.ConfigActivation, error) {
		return repo.SavePendingConfigActivation(ctx, ownerID, candidate, impacts)
	}
	activator.deps.activationSafe = func(context.Context, *db.SQLiteRepository) (bool, error) { return true, nil }
	activator.deps.build = activationTestBuild(&activationTestOwner{})
	activator.deps.cookies = activationTestCookies
	activator.deps.persistActivated = activateRuntimeConfigForTest
	pending, err := activator.savePending(t.Context(), "owner", &stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != api.ConfigActivationPending {
		t.Fatalf("pending = %#v", pending)
	}
	active, err := activator.ActivatePending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != api.ConfigActivationActive || active.ActiveGeneration != 1 || len(installer.generations) != 1 {
		t.Fatalf("matching pending candidate did not complete: activation=%#v installations=%d", active, len(installer.generations))
	}
	if _, _, err := repo.LoadPendingConfigActivationCandidate(t.Context()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("matching pending candidate remained: %v", err)
	}
}

func TestRuntimeActivatorPendingReplacementBeforeActivationSnapshotRetainsNewCandidate(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	installer := &activationTestInstaller{}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatal(err)
	}
	stored := validRuntimeActivationConfig()
	stored.MainSettings.DBPath = repo.DBPath()
	staleCandidate := stored
	staleCandidate.Metadata.KeepImages = true
	replacementCandidate := stored
	replacementCandidate.Metadata.OnlyID = true

	var stalePending api.ConfigActivation
	var replacement api.ConfigActivation
	var replacementPayload []byte
	replaceAtStoredLoad := false
	var staleFailureAttempts int
	builds := 0
	activator.deps.loadStored = func(ctx context.Context, _ *db.SQLiteRepository) (*config.Config, error) {
		if replaceAtStoredLoad {
			replaceAtStoredLoad = false
			failed, failErr := repo.FailPendingConfigActivation(ctx, stalePending.ActivationID, api.ConfigActivationFailureBuild)
			if failErr != nil {
				t.Fatalf("fail stale pending candidate: %v", failErr)
			}
			if failed.Status != api.ConfigActivationFailed || failed.ActivationID != stalePending.ActivationID {
				t.Fatalf("failed stale candidate = %#v", failed)
			}
			var saveErr error
			replacement, saveErr = activator.savePending(ctx, "replacement", &replacementCandidate, nil)
			if saveErr != nil {
				t.Fatalf("save replacement pending candidate: %v", saveErr)
			}
		}
		return &stored, nil
	}
	activator.deps.loadActivation = func(ctx context.Context, repo *db.SQLiteRepository) (api.ConfigActivation, error) {
		return repo.LoadConfigActivation(ctx)
	}
	activator.deps.savePending = func(
		ctx context.Context,
		repo *db.SQLiteRepository,
		ownerID string,
		candidate []byte,
		impacts []api.ConfigImpactDetail,
	) (api.ConfigActivation, error) {
		if ownerID == "replacement" {
			replacementPayload = slices.Clone(candidate)
		}
		return repo.SavePendingConfigActivation(ctx, ownerID, candidate, impacts)
	}
	activator.deps.failPending = func(
		ctx context.Context,
		repo *db.SQLiteRepository,
		activationID string,
		code api.ConfigActivationFailureCode,
	) (api.ConfigActivation, error) {
		staleFailureAttempts++
		return repo.FailPendingConfigActivation(ctx, activationID, code)
	}
	activator.deps.activationSafe = func(context.Context, *db.SQLiteRepository) (bool, error) { return true, nil }
	activator.deps.build = func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
		builds++
		return RuntimeGeneration{Owner: &activationTestOwner{}}, nil
	}
	activator.deps.cookies = activationTestCookies
	activator.deps.persistActivated = activateRuntimeConfigForTest

	stalePending, err = activator.savePending(t.Context(), "stale", &staleCandidate, nil)
	if err != nil {
		t.Fatalf("save stale pending candidate: %v", err)
	}
	replaceAtStoredLoad = true
	result, err := activator.ActivatePending(t.Context())
	if err != nil {
		t.Fatalf("activate stale pending candidate: %v", err)
	}
	if result.Status != api.ConfigActivationPending || result.ActivationID != replacement.ActivationID ||
		result.PendingGeneration != replacement.PendingGeneration {
		t.Fatalf("activation result = %#v, replacement = %#v", result, replacement)
	}
	if builds != 0 {
		t.Fatalf("stale candidate constructed %d runtime generations", builds)
	}
	if staleFailureAttempts != 1 {
		t.Fatalf("stale candidate failure attempts = %d, want 1", staleFailureAttempts)
	}
	if len(installer.generations) != 0 {
		t.Fatalf("installed stale candidate generations = %d", len(installer.generations))
	}
	payload, persisted, err := repo.LoadPendingConfigActivationCandidate(t.Context())
	if err != nil {
		t.Fatalf("load replacement pending candidate: %v", err)
	}
	if persisted.ActivationID != replacement.ActivationID || !slices.Equal(payload, replacementPayload) {
		t.Fatalf("persisted replacement = %#v payloadMatches=%t", persisted, slices.Equal(payload, replacementPayload))
	}
}

func TestRuntimeActivatorPendingReplacementDuringBuildRetiresStaleRuntime(t *testing.T) {
	repo := openRuntimeActivationTestRepo(t)
	installer := &activationTestInstaller{}
	activator, err := NewRuntimeActivator(repo, repo.DBPath(), installer)
	if err != nil {
		t.Fatal(err)
	}
	stored := validRuntimeActivationConfig()
	stored.MainSettings.DBPath = repo.DBPath()
	staleCandidate := stored
	staleCandidate.Metadata.KeepImages = true
	replacementCandidate := stored
	replacementCandidate.Metadata.OnlyID = true

	var stalePending api.ConfigActivation
	var replacement api.ConfigActivation
	var replacementPayload []byte
	var staleFailureAttempts int
	staleOwner := &activationTestOwner{}
	persistAttempts := 0
	var persistErr error
	activator.deps.loadStored = func(context.Context, *db.SQLiteRepository) (*config.Config, error) { return &stored, nil }
	activator.deps.loadActivation = func(ctx context.Context, repo *db.SQLiteRepository) (api.ConfigActivation, error) {
		return repo.LoadConfigActivation(ctx)
	}
	activator.deps.savePending = func(
		ctx context.Context,
		repo *db.SQLiteRepository,
		ownerID string,
		candidate []byte,
		impacts []api.ConfigImpactDetail,
	) (api.ConfigActivation, error) {
		if ownerID == "replacement" {
			replacementPayload = slices.Clone(candidate)
		}
		return repo.SavePendingConfigActivation(ctx, ownerID, candidate, impacts)
	}
	activator.deps.failPending = func(
		ctx context.Context,
		repo *db.SQLiteRepository,
		activationID string,
		code api.ConfigActivationFailureCode,
	) (api.ConfigActivation, error) {
		staleFailureAttempts++
		return repo.FailPendingConfigActivation(ctx, activationID, code)
	}
	activator.deps.activationSafe = func(context.Context, *db.SQLiteRepository) (bool, error) { return true, nil }
	activator.deps.build = func(ctx context.Context, _ config.Config, _ *db.SQLiteRepository) (RuntimeGeneration, error) {
		failed, failErr := repo.FailPendingConfigActivation(ctx, stalePending.ActivationID, api.ConfigActivationFailureBuild)
		if failErr != nil {
			t.Fatalf("fail stale pending candidate: %v", failErr)
		}
		if failed.Status != api.ConfigActivationFailed || failed.ActivationID != stalePending.ActivationID {
			t.Fatalf("failed stale candidate = %#v", failed)
		}
		var saveErr error
		replacement, saveErr = activator.savePending(ctx, "replacement", &replacementCandidate, nil)
		if saveErr != nil {
			t.Fatalf("save replacement pending candidate: %v", saveErr)
		}
		return RuntimeGeneration{Owner: staleOwner}, nil
	}
	activator.deps.cookies = activationTestCookies
	activator.deps.persistActivated = func(
		ctx context.Context,
		stored *config.Config,
		repo *db.SQLiteRepository,
		dbPath string,
		logger api.Logger,
		expected api.ConfigActivation,
		fingerprint api.WorkflowFingerprint,
		impacts []api.ConfigImpactDetail,
		transform db.ConfigActivationWorkflowTransform,
	) (api.ConfigActivation, error) {
		persistAttempts++
		activation, err := activateRuntimeConfigForTest(ctx, stored, repo, dbPath, logger, expected, fingerprint, impacts, transform)
		persistErr = err
		return activation, err
	}

	stalePending, err = activator.savePending(t.Context(), "stale", &staleCandidate, nil)
	if err != nil {
		t.Fatalf("save stale pending candidate: %v", err)
	}
	result, err := activator.ActivatePending(t.Context())
	if err != nil {
		t.Fatalf("activate stale pending candidate: %v", err)
	}
	if result.Status != api.ConfigActivationPending || result.ActivationID != replacement.ActivationID ||
		result.PendingGeneration != replacement.PendingGeneration {
		t.Fatalf("activation result = %#v, replacement = %#v", result, replacement)
	}
	if persistAttempts != 1 || !errors.Is(persistErr, api.ErrConfigActivationChanged) {
		t.Fatalf("stale persistence = attempts:%d err:%v", persistAttempts, persistErr)
	}
	if staleFailureAttempts != 1 {
		t.Fatalf("stale candidate failure attempts = %d, want 1", staleFailureAttempts)
	}
	if !staleOwner.closed.Load() {
		t.Fatal("stale runtime resources were not retired")
	}
	if len(installer.generations) != 0 {
		t.Fatalf("installed stale candidate generations = %d", len(installer.generations))
	}
	payload, persisted, err := repo.LoadPendingConfigActivationCandidate(t.Context())
	if err != nil {
		t.Fatalf("load replacement pending candidate: %v", err)
	}
	if persisted.ActivationID != replacement.ActivationID || !slices.Equal(payload, replacementPayload) {
		t.Fatalf("persisted replacement = %#v payloadMatches=%t", persisted, slices.Equal(payload, replacementPayload))
	}
}

func activateRuntimeConfigForTest(
	ctx context.Context,
	_ *config.Config,
	repo *db.SQLiteRepository,
	_ string,
	_ api.Logger,
	expected api.ConfigActivation,
	fingerprint api.WorkflowFingerprint,
	impacts []api.ConfigImpactDetail,
	transform db.ConfigActivationWorkflowTransform,
) (api.ConfigActivation, error) {
	tx, err := repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("begin test activation: %w", err)
	}
	activation, err := repo.ActivateConfigTx(ctx, tx, expected, fingerprint, impacts, transform)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("commit test activation: %w", err)
	}
	return activation, nil
}

func activationTestBuild(owner LifecycleOwner) func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error) {
	return func(_ context.Context, cfg config.Config, _ *db.SQLiteRepository) (RuntimeGeneration, error) {
		return RuntimeGeneration{Config: cfg, Owner: owner}, nil
	}
}

func activationTestCookies(context.Context, *db.SQLiteRepository, string, api.Logger) error {
	return nil
}

func assertActivationStage(t *testing.T, err error, want ActivationStage) {
	t.Helper()
	var activationErr *ActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error = %v, want ActivationError", err)
	}
	if activationErr.Stage != want {
		t.Fatalf("stage = %q, want %q", activationErr.Stage, want)
	}
}

func validRuntimeActivationConfig() config.Config {
	return config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x"},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
}

func openRuntimeActivationTestRepo(t *testing.T) *db.SQLiteRepository {
	t.Helper()
	repo, err := db.OpenWithLogger(filepath.Join(t.TempDir(), "runtime-activation.db"), api.NopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}
