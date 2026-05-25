// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guishared

import (
	"context"
	"errors"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// Runtime bundles the logger and core service built from a config snapshot.
// Both the GUI App and the web Backend reload their runtime when the user
// edits settings; this helper keeps the build-and-swap pattern in one place.
type Runtime struct {
	Core   api.Core
	Logger *logging.Logger
}

// BuildRuntime constructs a fresh logger and core service for cfg using the
// existing app/backend context and shared repository. On failure any partially
// initialized resources are cleaned up before returning.
func BuildRuntime(ctx context.Context, cfg config.Config, repo *db.SQLiteRepository) (Runtime, error) {
	if ctx == nil {
		return Runtime{}, errors.New("guishared: context is required")
	}

	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return Runtime{}, err
	}
	svc, err := core.NewWithContext(ctx, api.CoreDependencies{
		Context: ctx,
		Config:  cfg,
		Logger:  logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository: repo,
	})
	if err != nil {
		_ = logger.Close()
		return Runtime{}, err
	}
	return Runtime{Core: svc, Logger: logger}, nil
}
