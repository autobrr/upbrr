// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// testCoreOptions describes the dependency snapshot for a package test. The
// factory intentionally bypasses production config validation and default
// service construction while preserving the production composition graph.
type testCoreOptions struct {
	logger api.Logger
	repo   any
}

func newTestCore(opts testCoreOptions) *Core {
	logger := opts.logger
	if logger == nil {
		logger = api.NopLogger{}
	}

	repositories := api.RepositoryCapabilitiesFrom(opts.repo)
	core := &Core{logger: logger}
	core.history = newHistoryModule(repositories.History(), "", logger)
	return core
}

func TestNewTestCoreWiresHistoryModule(t *testing.T) {
	t.Parallel()

	core := newTestCore(testCoreOptions{})
	if core.history == nil {
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
