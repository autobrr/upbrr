// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
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
	if ctx == nil {
		return RuntimeGeneration{}, errors.New("webserver: context is required")
	}

	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return RuntimeGeneration{}, fmt.Errorf("web: %w", err)
	}
	svc, err := core.NewWithContext(ctx, api.CoreDependencies{
		Config: cfg,
		Logger: logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository:          repo.RepositoryCapabilities(),
		RepositoryOwner:     repo,
		SkipCookieMigration: true,
	})
	if err != nil {
		_ = logger.Close()
		return RuntimeGeneration{}, fmt.Errorf("web: %w", err)
	}
	capabilities, owner := BindCoreCapabilities(svc)
	return RuntimeGeneration{
		ID:           AllocateRuntimeGenerationID(),
		Config:       cfg,
		Capabilities: capabilities,
		Owner:        owner,
		Logger:       logger,
	}, nil
}
