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

// runtimeSnapshot returns a consistent copy of runtime fields guarded by runtimeMu.
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

// currentConfig returns the active runtime config under the runtime lock.
func (b *Backend) currentConfig() config.Config {
	if b == nil {
		return config.Config{}
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.cfg
}

// currentCore returns the active core service under the runtime lock.
func (b *Backend) currentCore() api.Core {
	if b == nil {
		return nil
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	return b.core
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

// logDebugf writes through the active logger while holding the runtime read
// lock so replacement cannot close the selected logger before the write
// completes.
func (b *Backend) logDebugf(format string, args ...any) {
	if b == nil {
		return
	}
	b.runtimeMu.RLock()
	defer b.runtimeMu.RUnlock()
	if b.logger != nil {
		b.logger.Debugf(format, args...)
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

// baseUploadOptions returns upload options from the current runtime config.
func (b *Backend) baseUploadOptions() api.UploadOptions {
	return buildBaseMetadataOptions(b.currentConfig())
}

// baseUploadOptions returns upload options derived from the same runtime
// snapshot as the core selected for a request.
func (rt backendRuntimeSnapshot) baseUploadOptions() api.UploadOptions {
	return buildBaseMetadataOptions(rt.cfg)
}

// replaceRuntime swaps all runtime-owned fields under one write lock and
// returns the previous core and logger for shutdown after callers finish
// follow-up work such as log stream rebinding.
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
