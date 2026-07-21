// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"path/filepath"
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
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}
