// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type runOptions struct {
	Debug       bool
	RunLogLevel string
}

func (a *App) buildRunOptions(debug bool, runLogLevel string) (runOptions, error) {
	if a == nil {
		return runOptions{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(runLogLevel) == "" {
		return runOptions{
			Debug:       debug,
			RunLogLevel: "",
		}, nil
	}

	normalized, err := api.ParseLogLevel(runLogLevel)
	if err != nil {
		return runOptions{}, fmt.Errorf("gui: %w", err)
	}

	return runOptions{
		Debug:       debug,
		RunLogLevel: normalized,
	}, nil
}

func (a *App) buildRunCore(opts runOptions) (api.Core, *logging.Logger, error) {
	if err := a.requireCore(); err != nil {
		return nil, nil, err
	}
	if a.repo == nil {
		return nil, nil, errors.New("config repository not initialized")
	}

	effectiveLogLevel := logging.ResolveEffectiveLevel(a.cfg.Logging.Level, opts.RunLogLevel, opts.Debug)
	logger, err := logging.NewWithLevel(a.cfg.Logging, a.cfg.MainSettings.DBPath, effectiveLogLevel)
	if err != nil {
		return nil, nil, fmt.Errorf("gui: %w", err)
	}

	coreSvc, err := core.New(api.CoreDependencies{
		Config: a.cfg,
		Logger: logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository: a.repo,
	})
	if err != nil {
		_ = logger.Close()
		return nil, nil, fmt.Errorf("gui: %w", err)
	}

	return coreSvc, logger, nil
}
func buildRunUploadOptions(cfg config.Config, opts runOptions) api.UploadOptions {
	return api.UploadOptions{
		Debug:           opts.Debug,
		DryRun:          opts.Debug,
		RunLogLevel:     opts.RunLogLevel,
		Screens:         cfg.ScreenshotHandling.Screens,
		SkipAutoTorrent: cfg.Metadata.SkipAutoTorrent,
		OnlyID:          cfg.Metadata.OnlyID,
		KeepImages:      cfg.Metadata.KeepImages,
	}
}
