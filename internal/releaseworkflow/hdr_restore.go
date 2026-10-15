// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func currentHDRResult(state *State) *api.HDRAnalysisResult {
	if ref := state.Workflow.HDRAnalysis; ref != nil {
		if result, ok := state.HDRAnalyses[ref.ID]; ok && result.Revision == ref.Revision {
			return &result
		}
	}
	return nil
}

func (m *Module) restorePendingHDR(
	ctx context.Context,
	owner string,
	state *State,
	release api.ReleaseRef,
	revision api.WorkflowRevision,
	now time.Time,
	capturedTargets []string,
) error {
	priorWorkflow, pending := state.PendingHDRAnalysisWorkflowID, state.PendingHDRAnalysis
	state.PendingHDRAnalysisWorkflowID, state.PendingHDRAnalysis = "", nil
	if priorWorkflow == "" {
		return nil
	}
	if request := state.HDRPendingRequest; request != nil && request.Instructions.Release == release && priorWorkflow == state.Workflow.ID {
		// An explicit interrupted request owns these checkpoint identities. A
		// same-generation preparation must not rebind them or replace its prior
		// result before the adapter resumes that request.
		state.Workflow.HDRAnalysis = pending
		state.Workflow.HDRAnalysisEnabled = false
		return nil
	}
	restorer, ok := m.hdrAnalysisBuilder.(CompatibleHDRExtractionRestorer)
	if !ok {
		return nil
	}
	source := state
	if priorWorkflow != state.Workflow.ID {
		loaded, err := m.repository.Load(ctx, owner, priorWorkflow)
		if errors.Is(err, ErrWorkflowNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load prior HDR workflow: %w", err)
		}
		source = &loaded
	}
	snapshot, err := currentReleaseSnapshot(state)
	if err != nil {
		return err
	}
	preparation := hdrPreparation(snapshot)
	selection := source.Workflow.HDRImages
	if pending != nil && priorWorkflow == state.Workflow.ID {
		if prior, exists := source.HDRAnalyses[pending.ID]; exists && prior.Revision == pending.Revision && prior.Release == release {
			if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok && validator.ValidateAuthority(ctx, preparation, prior) == nil &&
				m.retainedHDRPlotsValid(owner, state.Workflow.ID, prior, now) &&
				(len(capturedTargets) == 0 || slices.Equal(capturedTargets, prior.TargetIDs)) {
				if api.IsNativeHDRProfile(prior.ProfileVersion) {
					// Refresh derived status when reusing outcomes saved under older aggregation rules.
					prior.Targets = slices.Clone(prior.Targets)
					for i := range prior.Targets {
						prior.Targets[i].Status = prior.Targets[i].AnalysisStatus()
					}
					prior.Status = api.NativeHDRAggregate(prior.Targets)
					state.HDRAnalyses[prior.ID] = prior
				}
				state.Workflow.HDRAnalysis = pending
				state.Workflow.HDRAnalysisEnabled = prior.CanIncludeDefaultImages()
				if selection != nil {
					state.Workflow.HDRAnalysisEnabled = prior.CanIncludeImages() && len(selection.ArtifactIDs) > 0
				}
				state.Workflow.HDRImages = selection
				return nil
			}
		}
	}
	resources := make(map[api.HDRExtractionID]RetainedHDRExtractionResource)
	for id := range source.HDRExtractions {
		value, err := m.private.Get(owner, priorWorkflow, hdrExtractionPrivateResourceID(id), now)
		if err == nil {
			if resource, ok := value.(RetainedHDRExtractionResource); ok {
				resources[id] = resource
			}
		}
	}
	attempt, err := m.newID("hdr-rebind")
	if err != nil {
		return err
	}
	if operationID, ok := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID); ok && operationID != "" {
		attempt = string(operationID) + "-rebind"
	}
	if err := m.checkpointHDRAttempt(ctx, owner, state.Workflow.ID, release); err != nil {
		return err
	}
	entries := make(map[api.HDRExtractionID]HDRExtractionRecord)
	cloned := make(map[api.HDRExtractionID]RetainedHDRExtractionResource)
	var previous api.HDRAnalysisResult
	if pending != nil {
		previous = source.HDRAnalyses[pending.ID]
	}
	if priorWorkflow == state.Workflow.ID && pending != nil && previous.Revision == pending.Revision && previous.Release == release {
		// Numeric measurements bind native extraction IDs. Keep those identities
		// when only an existing generation's plots need regeneration.
		for id, record := range source.HDRExtractions {
			if record.Release == release && resources[id] != nil {
				entries[id] = record
			}
		}
	} else {
		entries, cloned, err = restorer.CloneExtractions(ctx, preparation, source.HDRExtractions, resources, attempt)
		if err != nil {
			return fmt.Errorf("restore HDR metadata: %w", err)
		}
	}
	var published []api.HDRExtractionID
	for id, resource := range cloned {
		if err := m.private.PutWithoutExpiry(owner, state.Workflow.ID, hdrExtractionPrivateResourceID(id), resource); err != nil {
			for _, other := range published {
				m.private.Delete(owner, state.Workflow.ID, hdrExtractionPrivateResourceID(other))
				delete(state.HDRExtractions, other)
			}
			for other, resource := range cloned {
				found := slices.Contains(published, other)
				if !found {
					releasePrivateResource(resource)
				}
			}
			return fmt.Errorf("publish rebound HDR metadata: %w", err)
		}
		published = append(published, id)
		state.HDRExtractions[id] = entries[id]
	}
	if len(cloned) > 0 {
		if err := m.repository.CheckpointHDRExtractions(ctx, owner, state.Workflow.ID, state.Workflow.Revision, state.HDRExtractions); err != nil {
			for id := range entries {
				m.private.Delete(owner, state.Workflow.ID, hdrExtractionPrivateResourceID(id))
				delete(state.HDRExtractions, id)
			}
			return fmt.Errorf("checkpoint rebound HDR metadata: %w", err)
		}
	}
	state.Workflow.HDRAnalysis, state.Workflow.HDRAnalysisEnabled = nil, false
	if pending == nil {
		return nil
	}
	prior, exists := source.HDRAnalyses[pending.ID]
	if !exists || prior.Revision != pending.Revision ||
		prior.ProfileVersion != api.HDRAnalysisProfileVersion && !api.IsNativeHDRProfile(prior.ProfileVersion) {
		return nil
	}
	if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok {
		current := prior
		current.Release = release
		if validator.ValidateAuthority(ctx, preparation, current) != nil {
			return nil
		}
	}
	var priorResource RetainedHDRAnalysisResource
	if value, err := m.private.Get(owner, priorWorkflow, hdrAnalysisPrivateResourceID(prior.AttemptID), now); err == nil {
		priorResource, _ = value.(RetainedHDRAnalysisResource)
	}
	// V2 restoration owns artifact integrity and terminal target outcomes in the
	// builder. Legacy numeric replay still requires retained evidence per target.
	if prior.ProfileVersion == api.HDRAnalysisProfileVersion {
		for _, target := range prior.TargetIDs {
			found := false
			if priorResource != nil {
				for _, previous := range prior.Targets {
					if previous.TargetID == target && previous.Status == api.StageStatusCompleted && previous.Artifact != nil {
						_, err := priorResource.LocalArtifactPath(prior, previous.Artifact.ID)
						found = err == nil
						break
					}
				}
			}
			for _, record := range entries {
				if record.TargetID == target {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
	}
	targets := prior.TargetIDs
	if len(capturedTargets) > 0 {
		targets = capturedTargets
	}
	decoded := prior.Decoded
	if prior.Release != release || priorWorkflow != state.Workflow.ID {
		// Native metadata may be rebound across preparation generations. Decoded
		// measurements and comparison references require their original authority;
		// preparation must not silently authorize new measurement passes.
		decoded = nil
	} else if decoded != nil && decoded.Comparison != nil && !decoded.Automatic {
		if _, err := m.resolveHDRComparison(owner, *state, api.HDRAnalysisInstructions{Release: release, Decoded: decoded}); err != nil {
			retained := *decoded
			retained.Comparison = nil
			decoded = &retained
			if !decoded.SceneSync && !decoded.ActiveArea {
				decoded = nil
			}
		}
	}
	_, err = m.analyzeHDR(ctx, owner, state, revision, now, AnalyzeHDRCommand{Instructions: api.HDRAnalysisInstructions{
		Release:        release,
		TargetIDs:      targets,
		PeakSource:     prior.PeakSource,
		ProfileVersion: prior.ProfileVersion,
		Formats:        prior.Formats,
		Decoded:        decoded,
	}}, &prior, priorResource)
	if err != nil {
		return err
	}
	if selection != nil && api.IsNativeHDRProfile(prior.ProfileVersion) {
		state.Workflow.HDRImages = remapHDRImages(selection, &prior, currentHDRResult(state))
		state.Workflow.HDRAnalysisEnabled = len(state.Workflow.HDRImages.ArtifactIDs) > 0
	}
	return nil
}

func (m *Module) retainedHDRPlotsValid(owner string, workflow api.WorkflowID, result api.HDRAnalysisResult, now time.Time) bool {
	value, err := m.private.Get(owner, workflow, hdrAnalysisPrivateResourceID(result.AttemptID), now)
	if err != nil {
		return false
	}
	resource, ok := value.(RetainedHDRAnalysisResource)
	if !ok {
		return false
	}
	for _, target := range result.Targets {
		for _, artifact := range target.Artifacts {
			if _, err := resource.LocalArtifactPath(result, artifact.ID); err != nil {
				return false
			}
		}
		if target.Artifact != nil {
			if _, err := resource.LocalArtifactPath(result, target.Artifact.ID); err != nil {
				return false
			}
		}
	}
	return true
}
