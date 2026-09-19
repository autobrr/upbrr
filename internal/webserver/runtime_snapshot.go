// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

// backendRuntimeSnapshot is a shallow, single-generation view of config and
// its bound runtime resources. It preserves request consistency without
// transferring ownership of the capability bundle, owner, or logger.
type backendRuntimeSnapshot struct {
	generationID uint64
	cfg          config.Config
	capabilities CoreCapabilities
	coreOwner    LifecycleOwner
	coreInitErr  error
	logger       *logging.Logger
	bundle       *runtimeBundle
	release      func()
}

// runtimeSnapshot copies all runtime fields under one read lock so callers do
// not combine config and capabilities from different settings generations.
func (b *Backend) runtimeSnapshot() backendRuntimeSnapshot {
	if b == nil {
		return backendRuntimeSnapshot{}
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return backendRuntimeSnapshot{
		generationID: b.runtimeGeneration,
		cfg:          b.cfg,
		capabilities: b.capabilities,
		coreOwner:    b.coreOwner,
		coreInitErr:  b.coreInitErr,
		logger:       b.logger,
		bundle:       b.runtimeBundle,
	}
}

// borrowRuntime acquires one immutable runtime generation for the operation.
// Callers must invoke the returned snapshot's release function after cleanup.
func (b *Backend) borrowRuntime() (backendRuntimeSnapshot, error) {
	b.runtimeAdmissionMu.RLock()
	rt, err := b.requireRuntime()
	if err != nil {
		b.runtimeAdmissionMu.RUnlock()
		return backendRuntimeSnapshot{}, err
	}
	release, ok := rt.bundle.borrow()
	if !ok {
		b.runtimeAdmissionMu.RUnlock()
		return backendRuntimeSnapshot{}, errors.New("runtime generation retired")
	}
	rt.release = func() {
		release()
		b.runtimeAdmissionMu.RUnlock()
	}
	return rt, nil
}

func (b *Backend) requireRuntime() (backendRuntimeSnapshot, error) {
	if b == nil {
		return backendRuntimeSnapshot{}, errors.New("backend not initialized")
	}
	rt := b.runtimeSnapshot()
	if rt.capabilities.Available() {
		return rt, nil
	}
	if rt.coreInitErr != nil {
		return backendRuntimeSnapshot{}, fmt.Errorf("core unavailable: %w", rt.coreInitErr)
	}
	return backendRuntimeSnapshot{}, errors.New("core not initialized")
}

// currentConfig returns the active runtime config under the runtime lock.
func (b *Backend) currentConfig() config.Config {
	if b == nil {
		return config.Config{}
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.cfg
}

func requireBackendCapability[T any](capability T, name string) (T, error) {
	if !CapabilityAvailable(capability) {
		var zero T
		return zero, fmt.Errorf("%s capability unavailable", name)
	}
	return capability, nil
}

func (rt backendRuntimeSnapshot) releaseWorkflowCore() (ReleaseWorkflowCapability, error) {
	return requireBackendCapability(rt.capabilities.ReleaseWorkflow, "release workflow")
}

func (rt backendRuntimeSnapshot) descriptionCore() (DescriptionCapability, error) {
	return requireBackendCapability(rt.capabilities.Description, "description")
}

func (rt backendRuntimeSnapshot) playlistCore() (PlaylistCapability, error) {
	return requireBackendCapability(rt.capabilities.Playlists, "playlist")
}

func (rt backendRuntimeSnapshot) historyCore() (HistoryCapability, error) {
	return requireBackendCapability(rt.capabilities.History, "history")
}

// currentLogger returns the active logger under the runtime lock.
func (b *Backend) currentLogger() *logging.Logger {
	if b == nil {
		return nil
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.logger
}

// logDebug writes through the active logger while holding the runtime read
// lock so replacement cannot close the selected logger before the write
// completes.
func (b *Backend) logDebug(message string) {
	if b == nil {
		return
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	if b.logger != nil {
		b.logger.Debugf("webserver: %s", message)
	}
}

// logInfof writes through the active logger while holding the runtime read lock.
func (b *Backend) logInfof(format string, args ...any) {
	if b == nil {
		return
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	if b.logger != nil {
		b.logger.Infof(format, args...)
	}
}

// logWarnf writes through the active logger while holding the runtime read lock.
func (b *Backend) logWarnf(format string, args ...any) {
	if b == nil {
		return
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	if b.logger != nil {
		b.logger.Warnf(format, args...)
	}
}

// logErrorf writes through the active logger while holding the runtime read lock.
func (b *Backend) logErrorf(format string, args ...any) {
	if b == nil {
		return
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	if b.logger != nil {
		b.logger.Errorf(format, args...)
	}
}

func (s *Server) logErrorf(format string, args ...any) {
	if s == nil || s.backend == nil {
		return
	}
	s.backend.logErrorf(format, args...)
}

func (b *Backend) replaceRuntimeGeneration(
	generationID uint64,
	cfg config.Config,
	capabilities CoreCapabilities,
	owner LifecycleOwner,
	logger *logging.Logger,
	bundle *runtimeBundle,
) (LifecycleOwner, *logging.Logger) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	oldOwner := b.coreOwner
	oldLogger := b.logger
	oldBundle := b.runtimeBundle
	b.capabilities = capabilities
	b.runtimeGeneration = generationID
	b.coreOwner = owner
	b.coreInitErr = nil
	b.logger = logger
	if bundle == nil {
		bundle = newRuntimeBundle(owner, logger)
	}
	b.runtimeBundle = bundle
	b.cfg = cfg
	if oldBundle != nil {
		return nil, oldLogger
	}
	return oldOwner, oldLogger
}

type backendRuntimeInstaller struct {
	backend *Backend
}

func (i backendRuntimeInstaller) Install(generation RuntimeGeneration) RetiredRuntime {
	oldBundle := i.backend.runtimeSnapshot().bundle
	oldOwner, oldLogger := i.backend.replaceRuntimeGeneration(
		generation.ID,
		generation.Config,
		generation.Capabilities,
		generation.Owner,
		generation.Logger,
		generation.Bundle,
	)
	if i.backend.hub != nil {
		i.backend.hub.SetLogger(generation.Logger)
	}
	i.backend.rebindLogStreams(oldLogger, generation.Logger)
	return RetiredRuntime{
		Bundle: oldBundle,
		Owner:  oldOwner,
		Logger: oldLogger,
	}
}

func (b *Backend) runtimeActivator() (*RuntimeActivator, error) {
	if b == nil {
		return nil, errors.New("backend not initialized")
	}
	b.activationInitMu.Lock()
	defer b.activationInitMu.Unlock()
	if b.activator != nil {
		return b.activator, nil
	}
	if b.repo == nil {
		return nil, errors.New("config repository not initialized")
	}
	activator, err := NewRuntimeActivator(b.repo, b.repo.DBPath(), backendRuntimeInstaller{backend: b})
	if err != nil {
		return nil, fmt.Errorf("initialize runtime activator: %w", err)
	}
	activator.currentConfig = b.currentConfig
	activator.deps.activationSafe = func(ctx context.Context, repo *db.SQLiteRepository) (bool, error) {
		if runtime := b.runtimeSnapshot(); runtime.bundle != nil && runtime.bundle.hasBorrowers() {
			return false, nil
		}
		return repo.ConfigActivationSafe(ctx)
	}
	activator.deps.acquireRuntimeAdmission = func() func() {
		b.runtimeAdmissionMu.Lock()
		return b.runtimeAdmissionMu.Unlock
	}
	activator.deps.loadActivation = func(ctx context.Context, repo *db.SQLiteRepository) (api.ConfigActivation, error) {
		return repo.LoadConfigActivation(ctx)
	}
	activator.deps.savePending = func(ctx context.Context, repo *db.SQLiteRepository, ownerID string, candidate []byte, impacts []api.ConfigImpactDetail) (api.ConfigActivation, error) {
		return repo.SavePendingConfigActivation(ctx, ownerID, candidate, impacts)
	}
	activator.deps.failPending = func(ctx context.Context, repo *db.SQLiteRepository, activationID string, code api.ConfigActivationFailureCode) (api.ConfigActivation, error) {
		return repo.FailPendingConfigActivation(ctx, activationID, code)
	}
	activator.deps.clearFailure = func(ctx context.Context, repo *db.SQLiteRepository, activationID string) (api.ConfigActivation, error) {
		return repo.ClearConfigActivationFailure(ctx, activationID)
	}
	activator.deps.transform = releaseworkflow.ApplyConfigImpact
	activator.deps.persistActivated = persistRuntimeConfigAndActivate
	if b.liveTest != nil {
		profile, err := livetest.ProfileForDB(b.repo.DBPath())
		if err != nil {
			return nil, fmt.Errorf("live-test activation profile: %w", err)
		}
		if profile.RunID != b.liveTest.RunID() {
			return nil, errors.New("live-test activation run identity mismatch")
		}
		registry, err := trackerimpl.NewRegistry()
		if err != nil {
			return nil, fmt.Errorf("live-test activation tracker registry: %w", err)
		}
		liveConfig := b.currentConfig()
		activator.liveProfile = &profile
		activator.liveImageConfig = liveConfig.ImageHosting
		activator.liveImageRegistry = registry
		activator.liveImageTrackerInputs = liveTestImageTrackerInputs(liveConfig, registry)
	}
	activator.deps.build = func(ctx context.Context, cfg config.Config, repo *db.SQLiteRepository) (RuntimeGeneration, error) {
		return buildRuntimeGenerationWithCoordinator(
			ctx, cfg, repo, b.liveTest, b.workflowCoordinator, activator.buildingConfigGeneration, activator.buildingConfigFingerprint,
		)
	}
	b.activator = activator
	return activator, nil
}
