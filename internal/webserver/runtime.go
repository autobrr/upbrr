// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// RuntimeGeneration groups one activated config generation's workflow bundle,
// lifecycle owner, and logger. Capabilities borrow the core; the runtime
// activator transfers Owner and Logger to the installer as one generation.
type RuntimeGeneration struct {
	ID           uint64
	Config       config.Config
	Capabilities CoreCapabilities
	Owner        LifecycleOwner
	Logger       *logging.Logger
	Bundle       *runtimeBundle
}

// runtimeBundle retains one immutable config generation until every borrower
// has released it. Its resources are closed exactly once after retirement.
type runtimeBundle struct {
	mu       sync.Mutex
	borrowed int
	retired  bool
	closed   bool
	owner    LifecycleOwner
	logger   *logging.Logger
}

func newRuntimeBundle(owner LifecycleOwner, logger *logging.Logger) *runtimeBundle {
	return &runtimeBundle{owner: owner, logger: logger}
}

func (b *runtimeBundle) setResources(owner LifecycleOwner, logger *logging.Logger) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.owner, b.logger = owner, logger
}

func (b *runtimeBundle) borrow() (func(), bool) {
	if b == nil {
		return func() {}, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.retired || b.closed {
		return nil, false
	}
	b.borrowed++
	return func() { b.release() }, true
}

func (b *runtimeBundle) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.borrowed--
	b.closeIfRetiredLocked()
}

func (b *runtimeBundle) hasBorrowers() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.borrowed > 0
}

func (b *runtimeBundle) retire() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retired = true
	b.closeIfRetiredLocked()
}

func (b *runtimeBundle) closeIfRetiredLocked() {
	if !b.retired || b.borrowed != 0 || b.closed {
		return
	}
	b.closed = true
	if b.owner != nil {
		_ = b.owner.Close()
	}
	if b.logger != nil {
		_ = b.logger.Close()
	}
}

var runtimeGenerationIDs atomic.Uint64

// AllocateRuntimeGenerationID returns a process-local monotonic identifier for
// one coherent runtime resource generation.
func AllocateRuntimeGenerationID() uint64 {
	return runtimeGenerationIDs.Add(1)
}

// buildRuntimeGeneration constructs a fresh generation from cfg using ctx and
// the shared repository. On failure it closes resources created before the
// error. Successful ownership transfers to the runtime activator.
func buildRuntimeGeneration(ctx context.Context, cfg config.Config, repo *db.SQLiteRepository) (RuntimeGeneration, error) {
	fingerprint, err := config.EffectiveConfigFingerprint(cfg)
	if err != nil {
		return RuntimeGeneration{}, fmt.Errorf("webserver: fingerprint runtime config: %w", err)
	}
	return buildRuntimeGenerationWithCoordinator(ctx, cfg, repo, nil, nil, 0, fingerprint)
}

func buildRuntimeGenerationWithCoordinator(
	ctx context.Context,
	cfg config.Config,
	repo *db.SQLiteRepository,
	policy *api.LiveTestPolicy,
	coordinator *releaseworkflow.Coordinator,
	configGeneration uint64,
	configFingerprint api.WorkflowFingerprint,
) (RuntimeGeneration, error) {
	if ctx == nil {
		return RuntimeGeneration{}, errors.New("webserver: context is required")
	}

	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return RuntimeGeneration{}, fmt.Errorf("web: %w", err)
	}
	bundle := newRuntimeBundle(nil, logger)
	svc, err := core.NewWithContextAndCoordinator(ctx, api.CoreDependencies{
		LiveTest: policy,
		Config:   cfg,
		Logger:   logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository:                        repo.RepositoryCapabilities(),
		RepositoryOwner:                   repo,
		SkipCookieMigration:               true,
		EnforceConfigActivationGeneration: true,
		ConfigActivationGeneration:        configGeneration,
		ConfigActivationFingerprint:       configFingerprint,
	}, coordinator)
	if err != nil {
		_ = logger.Close()
		return RuntimeGeneration{}, fmt.Errorf("web: %w", err)
	}
	capabilities, owner := BindCoreCapabilities(svc)
	bundle.setResources(owner, logger)
	svc.SetOperationLifetime(bundle.borrow)
	return RuntimeGeneration{
		ID:           AllocateRuntimeGenerationID(),
		Config:       cfg,
		Capabilities: capabilities,
		Owner:        owner,
		Logger:       logger,
		Bundle:       bundle,
	}, nil
}
