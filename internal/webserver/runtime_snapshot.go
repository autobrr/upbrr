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
	}
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

func (rt backendRuntimeSnapshot) metadataCore() (MetadataCapability, error) {
	return requireBackendCapability(rt.capabilities.Metadata, "metadata")
}

func (rt backendRuntimeSnapshot) releasePreparationCore() (ReleasePreparationCapability, error) {
	return requireBackendCapability(rt.capabilities.ReleasePreparation, "release preparation")
}

func (rt backendRuntimeSnapshot) selectionCore() (SelectionCapability, error) {
	return requireBackendCapability(rt.capabilities.Selection, "blu-ray selection")
}

func (rt backendRuntimeSnapshot) preparationCore() (PreparationCapability, error) {
	return requireBackendCapability(rt.capabilities.Preparation, "preparation")
}

func (rt backendRuntimeSnapshot) uploadReviewCore() (UploadReviewCapability, error) {
	return requireBackendCapability(rt.capabilities.UploadReview, "upload review")
}

func (rt backendRuntimeSnapshot) screenshotCore() (ScreenshotCapability, error) {
	return requireBackendCapability(rt.capabilities.Screenshots, "screenshot")
}

func (rt backendRuntimeSnapshot) hostedImageCore() (HostedImageCapability, error) {
	return requireBackendCapability(rt.capabilities.HostedImages, "hosted image")
}

func (rt backendRuntimeSnapshot) dvdMenuCore() (DVDCapability, error) {
	return requireBackendCapability(rt.capabilities.DVD, "DVD menu")
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

// baseUploadOptions returns upload options derived from the same runtime
// snapshot as the core selected for a request.
func (rt backendRuntimeSnapshot) baseUploadOptions() api.UploadOptions {
	return buildBaseMetadataOptions(rt.cfg)
}

// replaceRuntime swaps one complete runtime generation and returns the previous
// lifecycle owner and logger for separate shutdown after follow-up work such as
// log stream rebinding.
func (b *Backend) replaceRuntime(
	cfg config.Config,
	capabilities CoreCapabilities,
	logger *logging.Logger,
) (LifecycleOwner, *logging.Logger) {
	return b.replaceRuntimeGeneration(AllocateRuntimeGenerationID(), cfg, capabilities, nil, logger)
}

func (b *Backend) replaceRuntimeGeneration(
	generationID uint64,
	cfg config.Config,
	capabilities CoreCapabilities,
	owner LifecycleOwner,
	logger *logging.Logger,
) (LifecycleOwner, *logging.Logger) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	oldOwner := b.coreOwner
	oldLogger := b.logger
	b.capabilities = capabilities
	b.runtimeGeneration = generationID
	b.coreOwner = owner
	b.coreInitErr = nil
	b.logger = logger
	b.cfg = cfg
	return oldOwner, oldLogger
}

type backendRuntimeInstaller struct {
	backend *Backend
}

func (i backendRuntimeInstaller) Install(generation RuntimeGeneration) RetiredRuntime {
	oldOwner, oldLogger := i.backend.replaceRuntimeGeneration(
		generation.ID,
		generation.Config,
		generation.Capabilities,
		generation.Owner,
		generation.Logger,
	)
	if i.backend.hub != nil {
		i.backend.hub.SetLogger(generation.Logger)
	}
	i.backend.rebindLogStreams(oldLogger, generation.Logger)
	return RetiredRuntime{Owner: oldOwner, Logger: oldLogger}
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
	b.activator = activator
	return activator, nil
}
