// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// ActivationStage identifies the failed stage of a runtime activation.
type ActivationStage string

const (
	ActivationStageNormalize       ActivationStage = "normalize candidate"
	ActivationStageValidateStored  ActivationStage = "validate stored config"
	ActivationStageValidateRuntime ActivationStage = "validate runtime config"
	ActivationStageBuild           ActivationStage = "build runtime generation"
	ActivationStageCookies         ActivationStage = "maintain cookies"
	ActivationStagePersist         ActivationStage = "persist stored config"
)

// ActivationError preserves the failed activation stage and wrapped cause.
type ActivationError struct {
	Stage ActivationStage
	Err   error
}

func (e *ActivationError) Error() string {
	if e == nil {
		return "runtime activation failed"
	}
	return fmt.Sprintf("runtime activation: %s: %v", e.Stage, e.Err)
}

// Unwrap returns the stage cause.
func (e *ActivationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RetiredRuntime owns the resources replaced by one successful installation.
type RetiredRuntime struct {
	Owner  LifecycleOwner
	Logger *logging.Logger
}

// Close releases the replaced runtime after its installer has finished all
// transport-specific rebinding.
func (r RetiredRuntime) Close() {
	if r.Owner != nil {
		_ = r.Owner.Close()
	}
	if r.Logger != nil {
		_ = r.Logger.Close()
	}
}

// RuntimeInstaller atomically publishes one complete runtime generation and
// returns resources that can be retired after transport rebinding completes.
// Install must not fail.
type RuntimeInstaller interface {
	Install(RuntimeGeneration) RetiredRuntime
}

type runtimeActivationDeps struct {
	build   func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error)
	cookies func(context.Context, *db.SQLiteRepository, string, api.Logger) error
	persist func(context.Context, *config.Config, *db.SQLiteRepository, string) error
}

// RuntimeActivator serializes and owns the complete config-candidate to active
// runtime transition for one WebUI host.
type RuntimeActivator struct {
	mu          sync.Mutex
	repo        *db.SQLiteRepository
	fixedDBPath string
	installer   RuntimeInstaller
	deps        runtimeActivationDeps
}

// NewRuntimeActivator constructs an activator for one already-open repository
// and one host-specific runtime installer.
func NewRuntimeActivator(repo *db.SQLiteRepository, fixedDBPath string, installer RuntimeInstaller) (*RuntimeActivator, error) {
	if repo == nil {
		return nil, errors.New("runtime activation: repository is required")
	}
	fixedDBPath = strings.TrimSpace(fixedDBPath)
	if fixedDBPath == "" {
		return nil, errors.New("runtime activation: fixed database path is required")
	}
	if installer == nil {
		return nil, errors.New("runtime activation: installer is required")
	}
	return &RuntimeActivator{
		repo:        repo,
		fixedDBPath: fixedDBPath,
		installer:   installer,
		deps: runtimeActivationDeps{
			build:   buildRuntimeGeneration,
			cookies: maintainRuntimeCookies,
			persist: configstore.SaveToRepository,
		},
	}, nil
}

// Activate normalizes, validates, builds, maintains, persists, and publishes a
// config candidate as one serialized runtime transition.
func (a *RuntimeActivator) Activate(ctx context.Context, candidate config.Config) error {
	if a == nil {
		return errors.New("runtime activation: activator is required")
	}
	if ctx == nil {
		return errors.New("runtime activation: context is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	stored, err := cloneConfig(candidate)
	if err != nil {
		return activationError(ActivationStageNormalize, err)
	}
	if _, err := config.MergeMissingTrackerDefaults(stored); err != nil {
		return activationError(ActivationStageNormalize, err)
	}
	stored.MainSettings.DBPath = a.fixedDBPath
	if err := stored.Validate(); err != nil {
		return activationError(ActivationStageValidateStored, err)
	}

	runtimeCfg, err := cloneConfig(*stored)
	if err != nil {
		return activationError(ActivationStageNormalize, err)
	}
	config.ApplyEnvOverrides(runtimeCfg)
	runtimeCfg.MainSettings.DBPath = a.fixedDBPath
	if err := runtimeCfg.Validate(); err != nil {
		return activationError(ActivationStageValidateRuntime, err)
	}

	generation, err := a.deps.build(ctx, *runtimeCfg, a.repo)
	if err != nil {
		return activationError(ActivationStageBuild, err)
	}
	installed := false
	defer func() {
		if !installed {
			RetiredRuntime{Owner: generation.Owner, Logger: generation.Logger}.Close()
		}
	}()

	if err := a.deps.cookies(ctx, a.repo, a.fixedDBPath, generation.Logger); err != nil {
		return activationError(ActivationStageCookies, err)
	}
	if err := a.deps.persist(ctx, stored, a.repo, a.fixedDBPath); err != nil {
		return activationError(ActivationStagePersist, err)
	}

	generation.Config = *runtimeCfg
	retired := a.installer.Install(generation)
	installed = true
	retired.Close()
	return nil
}

func activationError(stage ActivationStage, err error) error {
	return &ActivationError{Stage: stage, Err: err}
}

func cloneConfig(source config.Config) (*config.Config, error) {
	payload, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("clone config: marshal: %w", err)
	}
	var cloned config.Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("clone config: unmarshal: %w", err)
	}
	return &cloned, nil
}

func maintainRuntimeCookies(ctx context.Context, repo *db.SQLiteRepository, dbPath string, logger api.Logger) error {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if err := validateRuntimeCookieAuth(dbPath); err != nil && !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		return fmt.Errorf("validate cookie auth: %w", err)
	}
	if err := cookies.SyncCookieEncryptionWithAuth(ctx, repo.RawDB(), dbPath); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("runtime activation: cookie encryption sync skipped: web auth helper unavailable")
		} else {
			return fmt.Errorf("sync cookie encryption: %w", err)
		}
	}
	cookiesDir, err := db.CookiePath(dbPath, "")
	if err != nil {
		logger.Debugf("runtime activation: cookie directory unavailable: %v", err)
		return nil
	}
	if err := cookies.EnsureCookieMigration(ctx, repo.RawDB(), dbPath, cookiesDir, logger); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("runtime activation: cookie migration skipped: web auth helper unavailable")
			return nil
		}
		return fmt.Errorf("migrate cookies: %w", err)
	}
	return nil
}

func validateRuntimeCookieAuth(dbPath string) error {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("load auth helper: %w", err)
	}
	if _, _, err := material.PrimaryHelper(); err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("derive auth helper: %w", err)
	}
	return nil
}
