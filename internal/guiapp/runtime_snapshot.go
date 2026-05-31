// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type appRuntimeSnapshot struct {
	cfg         config.Config
	core        api.Core
	coreInitErr error
	logger      *logging.Logger
}

func (a *App) runtimeSnapshot() appRuntimeSnapshot {
	if a == nil {
		return appRuntimeSnapshot{}
	}
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return appRuntimeSnapshot{
		cfg:         a.cfg,
		core:        a.core,
		coreInitErr: a.coreInitErr,
		logger:      a.logger,
	}
}

func (a *App) requireRuntime() (appRuntimeSnapshot, error) {
	if a == nil {
		return appRuntimeSnapshot{}, errors.New("app not initialized")
	}
	rt := a.runtimeSnapshot()
	if rt.core != nil {
		return rt, nil
	}
	if rt.coreInitErr != nil {
		return appRuntimeSnapshot{}, fmt.Errorf("core unavailable: %w", rt.coreInitErr)
	}
	return appRuntimeSnapshot{}, errors.New("core not initialized")
}

func (a *App) currentConfig() config.Config {
	if a == nil {
		return config.Config{}
	}
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.cfg
}

func (a *App) currentLogger() *logging.Logger {
	if a == nil {
		return nil
	}
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.logger
}

func (a *App) currentCore() api.Core {
	if a == nil {
		return nil
	}
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.core
}

func (a *App) baseUploadOptions() api.UploadOptions {
	return buildBaseUploadOptions(a.currentConfig())
}

func (a *App) replaceRuntime(cfg config.Config, core api.Core, logger *logging.Logger) (api.Core, *logging.Logger) {
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	oldCore := a.core
	oldLogger := a.logger
	a.core = core
	a.coreInitErr = nil
	a.logger = logger
	a.cfg = cfg
	return oldCore, oldLogger
}
