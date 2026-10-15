// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func hdrMeasurementPrivateResourceID(id string) string { return "hdr-measurement:" + id }

// HDRMeasurementStore carries generation-scoped checkpoint authority into the
// builder. Publish registers a closed report before later decoding or rendering.
type HDRMeasurementStore struct {
	// RetainedArtifactsOnly restores a previous run's outputs during preparation
	// without granting another decoded measurement or comparison-source read.
	RetainedArtifactsOnly bool
	// Nil grants a fresh explicit retry. A boundary limits a resumed retry to
	// older records; modes checkpointed after it are already consumed work.
	RetryAfterSequence *uint64
	// NativeRetryAfterSequence applies the same rule to source-extraction recovery.
	// A later compatible native checkpoint satisfies the pending recovery request.
	NativeRetryAfterSequence *uint64
	Comparison               *HDRComparisonSource
	Records                  map[string]HDRMeasurementRecord
	Resources                map[string]RetainedHDRMeasurementResource
	Publish                  func(HDRMeasurementRecord, RetainedHDRMeasurementResource) error
}

func (m *Module) hdrMeasurementStore(ctx context.Context, ownerID string, state *State, release api.ReleaseRef, now time.Time) HDRMeasurementStore {
	if state.HDRMeasurements == nil {
		state.HDRMeasurements = make(map[string]HDRMeasurementRecord)
	}
	store := HDRMeasurementStore{
		Records:   state.HDRMeasurements,
		Resources: make(map[string]RetainedHDRMeasurementResource),
	}
	for id, record := range store.Records {
		if record.Release != release {
			continue
		}
		value, err := m.private.Get(ownerID, state.Workflow.ID, hdrMeasurementPrivateResourceID(id), now)
		if err == nil {
			if resource, ok := value.(RetainedHDRMeasurementResource); ok {
				store.Resources[id] = resource
			}
		}
	}
	store.Publish = func(record HDRMeasurementRecord, resource RetainedHDRMeasurementResource) error {
		native, exists := state.HDRExtractions[record.ExtractionID]
		limit := int64(512 << 20)
		switch {
		case record.AudioTrackID != "":
			limit = 96 << 20
		case record.Result.Mode == api.HDRDecodedBoundarySync:
			limit = 16 << 20
		case record.Result.Mode == api.HDRDecodedActiveArea && record.Result.Coverage != "":
			limit = 8 << 20
		}
		if resource == nil || record.ID == "" || record.Release != release || record.Identity == "" ||
			!exists || native.Release != release || native.TargetID != record.TargetID || native.SourceFingerprint != record.SourceFingerprint ||
			native.SHA256 != record.ExtractionSHA256 || record.Size <= 0 || record.Size > limit || len(record.SHA256) != 64 ||
			(record.AudioTrackID == "") != (record.AudioScopeFingerprint == "") ||
			record.AudioTrackID != "" &&
				(len(record.AudioTrackID) > 128 || len(record.AudioScopeFingerprint) != 64 || record.Result.Mode != api.HDRDecodedAudio) ||
			record.Result.Validate() != nil || len(record.Result.Artifacts) != 0 {
			releasePrivateResource(resource)
			return ErrPrivateResourceIntegrity
		}
		if _, exists := state.HDRMeasurements[record.ID]; exists {
			releasePrivateResource(resource)
			return ErrPrivateResourceIntegrity
		}
		for _, prior := range state.HDRMeasurements {
			record.Sequence = max(record.Sequence, prior.Sequence)
		}
		if record.Sequence == ^uint64(0) {
			releasePrivateResource(resource)
			return ErrPrivateResourceIntegrity
		}
		record.Sequence++
		if err := ctx.Err(); err != nil {
			releasePrivateResource(resource)
			return fmt.Errorf("publish HDR measurement: %w", err)
		}
		if err := m.private.PutWithoutExpiry(ownerID, state.Workflow.ID, hdrMeasurementPrivateResourceID(record.ID), resource); err != nil {
			releasePrivateResource(resource)
			return fmt.Errorf("retain HDR measurement: %w", err)
		}
		state.HDRMeasurements[record.ID] = record
		if err := m.repository.CheckpointHDRMeasurements(ctx, ownerID, state.Workflow.ID, state.Workflow.Revision, state.HDRMeasurements); err != nil {
			delete(state.HDRMeasurements, record.ID)
			m.private.Delete(ownerID, state.Workflow.ID, hdrMeasurementPrivateResourceID(record.ID))
			return fmt.Errorf("checkpoint HDR measurement: %w", err)
		}
		store.Resources[record.ID] = resource
		return nil
	}
	return store
}
