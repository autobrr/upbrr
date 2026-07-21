// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testCoreOptions describes the dependency snapshot for a package test. The
// factory intentionally bypasses production config validation and default
// service construction while preserving the production composition graph.
type testCoreOptions struct {
	cfg      config.Config
	logger   api.Logger
	services api.ServiceSet
	repo     any
	registry *trackers.Registry
}

func newTestCore(opts testCoreOptions) *Core {
	logger := opts.logger
	if logger == nil {
		logger = api.NopLogger{}
	}

	repositories := api.RepositoryCapabilitiesFrom(opts.repo)
	core := &Core{logger: logger, selections: repositories.Selections()}
	core.history = newHistoryModule(repositories.History(), opts.cfg.MainSettings.DBPath, logger)
	core.description = newDescriptionModule(opts.cfg, logger, opts.services, repositories.Selections(), opts.registry, core.preparedFacts)
	var mediaRepo mediaRepository
	if repositories.Trackers() != nil && repositories.Media() != nil {
		mediaRepo = mediaRepositoryView{TrackerStateRepository: repositories.Trackers(), MediaAssetRepository: repositories.Media()}
	}
	core.media = newMediaModule(opts.cfg, logger, opts.services, mediaRepo, opts.registry, core.preparedFacts)
	core.upload = newUploadModule(
		opts.cfg,
		logger,
		opts.services,
		repositories.ReleaseState(),
		repositories.Trackers(),
		opts.registry,
		core.preparedFacts,
		core.description.resolveOverrideRequest,
		core.description.resolveSubjectGroups,
		core.ImportAcceptedMenuImages,
	)
	core.dupe = newDupeModule(opts.cfg, logger, opts.services, opts.registry, core.preparedFacts)
	return core
}

func TestNewTestCoreWiresCanonicalModules(t *testing.T) {
	t.Parallel()

	core := newTestCore(testCoreOptions{})
	if core.history == nil || core.description == nil || core.media == nil || core.upload == nil || core.dupe == nil {
		t.Fatal("test Core composition is incomplete")
	}
}

type stubRepo struct{}

func (stubRepo) ListHistoryEntries(context.Context) ([]api.HistoryEntry, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) LoadHistoryRecord(context.Context, string) (api.HistoryRecord, error) {
	return api.HistoryRecord{}, internalerrors.ErrNotImplemented
}

func (stubRepo) LoadHistoryCleanupSnapshot(context.Context, string) (api.HistoryCleanupSnapshot, error) {
	return api.HistoryCleanupSnapshot{}, internalerrors.ErrNotImplemented
}

func (stubRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (stubRepo) PurgeContentData(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}
