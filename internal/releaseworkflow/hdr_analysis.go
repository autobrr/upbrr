// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func WithHDRAnalysisBuilder(builder HDRAnalysisBuilder) Option {
	return func(m *Module) error { m.hdrAnalysisBuilder = builder; return nil }
}

func hdrAnalysisPrivateResourceID(attemptID string) string {
	return "hdr-analysis:" + strings.TrimSpace(attemptID)
}
func hdrExtractionPrivateResourceID(id api.HDRExtractionID) string {
	return "hdr-extraction:" + string(id)
}

func (m *Module) analyzeHDR(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command AnalyzeHDRCommand,
	prior *api.HDRAnalysisResult,
	priorResource RetainedHDRAnalysisResource,
) (CommandResult, error) {
	if m.hdrAnalysisBuilder == nil {
		return CommandResult{}, fmt.Errorf("%w: HDR analysis is unavailable", ErrInvalidTransition)
	}
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return CommandResult{}, fmt.Errorf("hdr_analysis: %w", err)
	}
	releaseRef := api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	normalized, err := command.Instructions.Normalize()
	if err != nil || normalized.Release != releaseRef {
		return CommandResult{}, fmt.Errorf("%w: HDR analysis must select the current prepared release", ErrInvalidTransition)
	}
	attemptID := ""
	if operationID, ok := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID); ok {
		attemptID = string(operationID)
	}
	if attemptID == "" {
		attemptID, err = m.newID("hdr-attempt")
		if err != nil {
			return CommandResult{}, fmt.Errorf("hdr_analysis: %w", err)
		}
	}
	if state.HDRExtractions == nil {
		state.HDRExtractions = make(map[api.HDRExtractionID]HDRExtractionRecord)
	}
	if state.HDRAnalyses == nil {
		state.HDRAnalyses = make(map[api.HDRAnalysisResultID]api.HDRAnalysisResult)
	}
	resources := make(map[api.HDRExtractionID]RetainedHDRExtractionResource)
	for id, record := range state.HDRExtractions {
		if record.Release != releaseRef {
			continue
		}
		value, getErr := m.private.Get(ownerID, state.Workflow.ID, hdrExtractionPrivateResourceID(id), now)
		if getErr == nil {
			if resource, ok := value.(RetainedHDRExtractionResource); ok {
				resources[id] = resource
			}
		}
	}
	publish := func(record HDRExtractionRecord, resource RetainedHDRExtractionResource) error {
		if record.Release != releaseRef || record.ID == "" || resource == nil {
			return ErrPrivateResourceIntegrity
		}
		if _, exists := state.HDRExtractions[record.ID]; exists {
			return ErrPrivateResourceIntegrity
		}
		if err := ctx.Err(); err != nil {
			releasePrivateResource(resource)
			return fmt.Errorf("publish HDR extraction: %w", err)
		}
		if err := m.private.PutWithoutExpiry(ownerID, state.Workflow.ID, hdrExtractionPrivateResourceID(record.ID), resource); err != nil {
			releasePrivateResource(resource)
			return fmt.Errorf("hdr_analysis: %w", err)
		}
		state.HDRExtractions[record.ID] = record
		if err := m.repository.CheckpointHDRExtractions(ctx, ownerID, state.Workflow.ID, state.Workflow.Revision, state.HDRExtractions); err != nil {
			delete(state.HDRExtractions, record.ID)
			m.private.Delete(ownerID, state.Workflow.ID, hdrExtractionPrivateResourceID(record.ID))
			return fmt.Errorf("checkpoint HDR extraction: %w", err)
		}
		return nil
	}
	if ref := state.Workflow.HDRAnalysis; prior == nil && ref != nil {
		if analysis, ok := state.HDRAnalyses[ref.ID]; ok && analysis.Revision == ref.Revision {
			prior = &analysis
			value, getErr := m.private.Get(ownerID, state.Workflow.ID, hdrAnalysisPrivateResourceID(analysis.AttemptID), now)
			if getErr == nil {
				priorResource, _ = value.(RetainedHDRAnalysisResource)
			}
		}
	}
	var retired []api.HDRExtractionID
	if prior != nil && prior.Release == releaseRef {
		for id, record := range state.HDRExtractions {
			if record.Release == releaseRef && slices.Contains(normalized.TargetIDs, record.TargetID) && prior.TargetNeedsSourceRetry(record.TargetID) {
				delete(state.HDRExtractions, id)
				delete(resources, id)
				retired = append(retired, id)
			}
		}
	}
	if len(retired) > 0 {
		if err := m.repository.CheckpointHDRExtractions(ctx, ownerID, state.Workflow.ID, state.Workflow.Revision, state.HDRExtractions); err != nil {
			return CommandResult{}, fmt.Errorf("checkpoint retired HDR metadata: %w", err)
		}
		for _, id := range retired {
			m.private.Delete(ownerID, state.Workflow.ID, hdrExtractionPrivateResourceID(id))
		}
	}
	snapshot, resource, err := m.hdrAnalysisBuilder.Build(
		ctx,
		releaseRef,
		normalized,
		attemptID,
		now,
		prior,
		priorResource,
		state.HDRExtractions,
		resources,
		publish,
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow analyze HDR: %w", err)
	}
	id, err := m.newID("hdr-analysis")
	if err != nil {
		releasePrivateResource(resource)
		return CommandResult{}, fmt.Errorf("hdr_analysis: %w", err)
	}
	snapshot.ID, snapshot.WorkflowID, snapshot.Revision = api.HDRAnalysisResultID(id), state.Workflow.ID, nextRevision
	snapshot.Release, snapshot.AttemptID = releaseRef, attemptID
	if err := snapshot.Validate(); err != nil {
		releasePrivateResource(resource)
		return CommandResult{}, fmt.Errorf("publish HDR analysis: %w", err)
	}
	if resource != nil {
		if err := m.private.PutWithoutExpiry(ownerID, state.Workflow.ID, hdrAnalysisPrivateResourceID(attemptID), resource); err != nil {
			releasePrivateResource(resource)
			return CommandResult{}, fmt.Errorf("hdr_analysis: %w", err)
		}
	}
	state.HDRAnalyses[snapshot.ID] = snapshot
	state.Workflow.HDRAnalysis = &api.HDRAnalysisRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.HDRAnalysisEnabled = snapshot.Status == api.StageStatusCompleted
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	return CommandResult{HDRAnalysis: &snapshot}, nil
}

func (m *Module) setHDRAnalysisEnabled(ctx context.Context, state *State, command SetHDRAnalysisEnabledCommand) error {
	if command.Enabled {
		if state.Workflow.HDRAnalysis == nil {
			return fmt.Errorf("%w: generate HDR analysis before enabling inclusion", ErrInvalidTransition)
		}
		analysis, ok := state.HDRAnalyses[state.Workflow.HDRAnalysis.ID]
		if !ok || analysis.Revision != state.Workflow.HDRAnalysis.Revision || analysis.Status != api.StageStatusCompleted {
			return fmt.Errorf("%w: every HDR target must complete before inclusion", ErrInvalidTransition)
		}
		if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok {
			if err := validator.ValidateAuthority(ctx, analysis); err != nil {
				return fmt.Errorf("hdr_analysis: %w", err)
			}
		}
		value, err := m.private.Get(state.OwnerID, state.Workflow.ID, hdrAnalysisPrivateResourceID(analysis.AttemptID), m.clock.Now().UTC())
		if err != nil {
			return fmt.Errorf("hdr_analysis: %w", err)
		}
		resource, ok := value.(RetainedHDRAnalysisResource)
		if !ok {
			return ErrPrivateResourceUnavailable
		}
		for _, target := range analysis.Targets {
			if target.Artifact == nil {
				return ErrPrivateResourceIntegrity
			}
			if _, err := resource.LocalArtifactPath(analysis, target.Artifact.ID); err != nil {
				return fmt.Errorf("hdr_analysis: %w", err)
			}
		}
	}
	if !command.Enabled && state.Composite != nil {
		state.Composite.Intent.HDRAnalysis = nil
	}
	state.Workflow.HDRAnalysisEnabled = command.Enabled
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	return nil
}

func (m *Module) hdrAnalysisResource(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	ref api.HDRAnalysisRef,
) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" || ref.ID == "" || ref.Revision == 0 {
		return api.HDRAnalysisResult{}, nil, ErrWorkflowNotFound
	}
	lock := m.commandLock(ownerID + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("hdr_analysis: %w", err)
	}
	if state.Workflow.HDRAnalysis == nil || *state.Workflow.HDRAnalysis != ref {
		return api.HDRAnalysisResult{}, nil, ErrRevisionConflict
	}
	analysis, ok := state.HDRAnalyses[ref.ID]
	if ok && analysis.Validate() != nil {
		return api.HDRAnalysisResult{}, nil, ErrPrivateResourceIntegrity
	}
	if !ok || analysis.Revision != ref.Revision || state.Workflow.Release == nil {
		return api.HDRAnalysisResult{}, nil, ErrRevisionConflict
	}
	release, err := currentReleaseSnapshot(&state)
	if err != nil || analysis.Release != (api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}) {
		return api.HDRAnalysisResult{}, nil, ErrRevisionConflict
	}
	if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok {
		if err := validator.ValidateAuthority(ctx, analysis); err != nil {
			return api.HDRAnalysisResult{}, nil, fmt.Errorf("hdr_analysis: %w", err)
		}
	}
	value, err := m.private.Get(ownerID, workflowID, hdrAnalysisPrivateResourceID(analysis.AttemptID), m.clock.Now().UTC())
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("hdr_analysis: %w", err)
	}
	resource, ok := value.(RetainedHDRAnalysisResource)
	if !ok {
		return api.HDRAnalysisResult{}, nil, ErrPrivateResourceUnavailable
	}
	return analysis, resource, nil
}

func (m *Module) HDRAnalysisArtifact(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	ref api.HDRAnalysisRef,
	artifactID api.PublicResourceID,
) (MediaArtifactContent, error) {
	analysis, resource, err := m.hdrAnalysisResource(ctx, ownerID, workflowID, ref)
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("hdr_analysis: %w", err)
	}
	content, err := resource.OpenArtifact(ctx, analysis, artifactID)
	if err != nil {
		return MediaArtifactContent{}, fmt.Errorf("open HDR artifact: %w", err)
	}
	return content, nil
}

func (m *Module) HDRAnalysisArtifactPath(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	ref api.HDRAnalysisRef,
	artifactID api.PublicResourceID,
) (string, error) {
	analysis, resource, err := m.hdrAnalysisResource(ctx, ownerID, workflowID, ref)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis: %w", err)
	}
	value, err := resource.LocalArtifactPath(analysis, artifactID)
	if err != nil {
		return "", fmt.Errorf("resolve HDR artifact path: %w", err)
	}
	return value, nil
}

func hdrStoppedResult(command mutation, result CommandResult) bool {
	_, hdr := command.(AnalyzeHDRCommand)
	return hdr && result.HDRAnalysis != nil && (result.HDRAnalysis.Status == api.StageStatusCanceled || result.HDRAnalysis.Status == api.StageStatusInterrupted)
}

func (m *Module) cleanupInterruptedHDR(ctx context.Context, record api.ReleaseWorkflowOperationRecord) error {
	if record.Status.Command != "analyze_hdr" && record.Status.Command != "composite_upload" && record.Status.Command != "prepare_release" &&
		record.Status.Command != "reset_release" &&
		record.Status.Command != "select_bluray_candidate" {
		return nil
	}
	cleaner, ok := m.hdrAnalysisBuilder.(HDRAnalysisAttemptCleaner)
	if !ok {
		return nil
	}
	state, err := m.repository.Load(ctx, record.OwnerID, record.WorkflowID)
	if err != nil {
		if errors.Is(err, ErrWorkflowNotFound) {
			return nil
		}
		return fmt.Errorf("hdr_analysis: %w", err)
	}
	attempt := string(record.OperationID)
	for _, result := range state.HDRAnalyses {
		if result.AttemptID == attempt {
			return nil
		}
	}
	m.private.Delete(record.OwnerID, record.WorkflowID, hdrAnalysisPrivateResourceID(attempt))
	seen := make(map[api.ReleaseRef]bool)
	for _, snapshot := range state.Releases {
		ref := api.ReleaseRef{SourcePath: snapshot.Release.Source.SourcePath, Generation: snapshot.Release.Generation}
		if seen[ref] || ref.Generation == 0 || ref.SourcePath == "" {
			continue
		}
		seen[ref] = true
		if err := cleaner.CleanupAttempt(ref, attempt, state.HDRExtractions); err != nil {
			return fmt.Errorf("clean interrupted HDR files: %w", err)
		}
	}
	return nil
}
