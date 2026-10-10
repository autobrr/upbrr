// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// CheckpointHDRExtractions persists complete extraction authority without publishing a plot or advancing user intent.
func (r *PersistentRepository) CheckpointHDRExtractions(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	revision api.WorkflowRevision,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
) error {
	state, err := r.Load(ctx, ownerID, workflowID)
	if err != nil {
		return err
	}
	if state.Workflow.Revision != revision {
		return ErrRevisionConflict
	}
	state.HDRExtractions = maps.Clone(entries)
	record, err := workflowStateRecord(ownerID, state)
	if err != nil {
		return err
	}
	if err := r.states.CheckpointReleaseWorkflowResources(ctx, record); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

// CheckpointHDRExtractions retains extraction authority at the expected revision without advancing user intent.
func (r *MemoryRepository) CheckpointHDRExtractions(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	revision api.WorkflowRevision,
	entries map[api.HDRExtractionID]HDRExtractionRecord,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("checkpoint HDR extraction: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.states[workflowID]
	if !ok || state.OwnerID != strings.TrimSpace(ownerID) {
		return ErrWorkflowNotFound
	}
	if state.Workflow.Revision != revision {
		return ErrRevisionConflict
	}
	state.HDRExtractions = maps.Clone(entries)
	r.states[workflowID] = state
	return nil
}
