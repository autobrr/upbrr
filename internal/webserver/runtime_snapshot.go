// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type backendRuntimeSnapshot struct {
	cfg         config.Config
	core        api.Core
	coreInitErr error
	logger      *logging.Logger
}

func (b *Backend) runtimeSnapshot() backendRuntimeSnapshot {
	if b == nil {
		return backendRuntimeSnapshot{}
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return backendRuntimeSnapshot{
		cfg:         b.cfg,
		core:        b.core,
		coreInitErr: b.coreInitErr,
		logger:      b.logger,
	}
}

func (b *Backend) requireRuntime() (backendRuntimeSnapshot, error) {
	if b == nil {
		return backendRuntimeSnapshot{}, errors.New("backend not initialized")
	}
	rt := b.runtimeSnapshot()
	if rt.core != nil {
		return rt, nil
	}
	if rt.coreInitErr != nil {
		return backendRuntimeSnapshot{}, fmt.Errorf("core unavailable: %w", rt.coreInitErr)
	}
	return backendRuntimeSnapshot{}, errors.New("core not initialized")
}

func (b *Backend) currentConfig() config.Config {
	if b == nil {
		return config.Config{}
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.cfg
}

func (b *Backend) currentCore() api.Core {
	if b == nil {
		return nil
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.core
}

func (b *Backend) currentLogger() *logging.Logger {
	if b == nil {
		return nil
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.logger
}

func (b *Backend) baseUploadOptions() api.UploadOptions {
	return buildBaseMetadataOptions(b.currentConfig())
}

func (b *Backend) replaceRuntime(cfg config.Config, core api.Core, logger *logging.Logger) (api.Core, *logging.Logger) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	oldCore := b.core
	oldLogger := b.logger
	b.core = core
	b.coreInitErr = nil
	b.logger = logger
	b.cfg = cfg
	return oldCore, oldLogger
}
