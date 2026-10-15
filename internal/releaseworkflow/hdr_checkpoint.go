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

// CheckpointHDRMeasurements retains completed numeric evidence at the current
// revision, through the same fenced state transaction as native extractions.
func (r *PersistentRepository) CheckpointHDRMeasurements(
	ctx context.Context, ownerID string, workflowID api.WorkflowID, revision api.WorkflowRevision,
	entries map[string]HDRMeasurementRecord,
) error {
	state, err := r.Load(ctx, ownerID, workflowID)
	if err != nil {
		return err
	}
	if state.Workflow.Revision != revision {
		return ErrRevisionConflict
	}
	state.HDRMeasurements = cloneHDRMeasurements(entries)
	record, err := workflowStateRecord(ownerID, state)
	if err != nil {
		return err
	}
	if err := r.states.CheckpointReleaseWorkflowResources(ctx, record); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

func (r *MemoryRepository) CheckpointHDRMeasurements(
	ctx context.Context, ownerID string, workflowID api.WorkflowID, revision api.WorkflowRevision,
	entries map[string]HDRMeasurementRecord,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("checkpoint HDR measurement: %w", err)
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
	state.HDRMeasurements = cloneHDRMeasurements(entries)
	r.states[workflowID] = state
	return nil
}

func cloneHDRMeasurements(entries map[string]HDRMeasurementRecord) map[string]HDRMeasurementRecord {
	cloned := maps.Clone(entries)
	for id, record := range cloned {
		record.Result.Artifacts = append([]api.PublicResourceID(nil), record.Result.Artifacts...)
		cloned[id] = record
	}
	return cloned
}

// CheckpointHDRRequest retains explicit request authority before builder work,
// using the same revision and active-input fence as extraction checkpoints.
func (r *PersistentRepository) CheckpointHDRRequest(
	ctx context.Context, ownerID string, workflowID api.WorkflowID, revision api.WorkflowRevision, pending PendingHDRRequest,
) error {
	state, err := r.Load(ctx, ownerID, workflowID)
	if err != nil {
		return err
	}
	if state.Workflow.Revision != revision {
		return ErrRevisionConflict
	}
	if err := retainHDRRequest(&state, pending); err != nil {
		return err
	}
	record, err := workflowStateRecord(ownerID, state)
	if err != nil {
		return err
	}
	if err := r.states.CheckpointReleaseWorkflowResources(ctx, record); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

func (r *MemoryRepository) CheckpointHDRRequest(
	ctx context.Context, ownerID string, workflowID api.WorkflowID, revision api.WorkflowRevision, pending PendingHDRRequest,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("checkpoint HDR request: %w", err)
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
	if err := retainHDRRequest(&state, pending); err != nil {
		return err
	}
	r.states[workflowID] = state
	return nil
}

func retainHDRRequest(state *State, pending PendingHDRRequest) error {
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return err
	}
	instructions, err := pending.Instructions.Normalize()
	if err != nil || instructions.Release != hdrPreparation(release).Release {
		return ErrInvalidTransition
	}
	pending.Instructions = instructions
	if pending.NativeRetryAfterSequence != nil {
		pending.NativeRetryAfterSequence = new(*pending.NativeRetryAfterSequence)
	}
	state.HDRPendingRequest = &pending
	return nil
}
