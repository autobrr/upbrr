// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonSourceDiscoverer reads programme facts without publishing workflow
// state or mutating the applied comparison intent.
type ComparisonSourceDiscoverer interface {
	DiscoverComparisonSource(context.Context, api.ReleaseRef, string, string, bool) (api.ComparisonSourceDiscovery, error)
}

// DiscoverComparisonSource synchronizes discovery with source/workflow changes.
func (m *Module) DiscoverComparisonSource(
	ctx context.Context,
	owner string,
	request api.DiscoverReleaseWorkflowComparisonSourceRequest,
) (api.ComparisonSourceDiscovery, error) {
	if err := request.Validate(); err != nil {
		return api.ComparisonSourceDiscovery{}, fmt.Errorf("comparison source discovery: %w", err)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return api.ComparisonSourceDiscovery{}, ErrWorkflowNotFound
	}
	ctx, err := m.activeMutationContext(ctx, owner, request.WorkflowID)
	if err != nil {
		return api.ComparisonSourceDiscovery{}, err
	}
	if err := m.requireActiveConfig(ctx); err != nil {
		return api.ComparisonSourceDiscovery{}, err
	}
	discoverer, ok := m.mediaBuilder.(ComparisonSourceDiscoverer)
	if !ok {
		return api.ComparisonSourceDiscovery{}, ErrInvalidTransition
	}
	lock := m.commandLock(owner + "\x00" + string(request.WorkflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, owner, request.WorkflowID)
	if err != nil {
		return api.ComparisonSourceDiscovery{}, fmt.Errorf("comparison source discovery: %w", err)
	}
	if state.Workflow.Revision != request.ExpectedRevision {
		return api.ComparisonSourceDiscovery{}, ErrRevisionConflict
	}
	prepared, err := currentReleaseSnapshot(&state)
	if err != nil {
		return api.ComparisonSourceDiscovery{}, err
	}
	mainID := "main"
	media, _, err := m.mediaExtensionSnapshot(owner, &state, request.Media, m.clock.Now().UTC())
	if err != nil {
		return api.ComparisonSourceDiscovery{}, err
	}
	if media != nil && media.Comparisons != nil {
		mainID = media.Comparisons.MainSourceID
	}
	discovery, err := discoverer.DiscoverComparisonSource(
		ctx,
		api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation},
		request.SourceID,
		request.Path,
		request.SourceID == mainID,
	)
	if err != nil {
		return api.ComparisonSourceDiscovery{}, fmt.Errorf("discover comparison programme: %w", err)
	}
	return discovery, nil
}
