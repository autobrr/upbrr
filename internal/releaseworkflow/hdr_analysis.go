// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// WithHDRAnalysisBuilder supplies native analysis, checkpointing and artifact production.
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
	var extractionSequence uint64
	for _, record := range state.HDRExtractions {
		extractionSequence = max(extractionSequence, record.Sequence)
	}
	publish := func(record HDRExtractionRecord, resource RetainedHDRExtractionResource) error {
		if record.Release != releaseRef || record.ID == "" || resource == nil {
			return ErrPrivateResourceIntegrity
		}
		if _, exists := state.HDRExtractions[record.ID]; exists {
			return ErrPrivateResourceIntegrity
		}
		record.Sequence = max(record.Sequence, extractionSequence)
		if record.Sequence == ^uint64(0) {
			return ErrPrivateResourceIntegrity
		}
		record.Sequence++
		extractionSequence = record.Sequence
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
	if retry := normalized.DecodedRetry; retry != nil {
		if prior == nil || prior.AttemptID != retry.AnalysisAttemptID || prior.Release != releaseRef ||
			prior.ProfileVersion != normalized.ProfileVersion || prior.Formats != normalized.Formats ||
			!slices.Equal(prior.TargetIDs, normalized.TargetIDs) || !reflect.DeepEqual(prior.Decoded, normalized.Decoded) {
			return CommandResult{}, fmt.Errorf("%w: decoded retry must select the current retained attempt and options", ErrInvalidTransition)
		}
		for _, mode := range retry.Modes {
			eligible := false
			for _, target := range prior.Targets {
				for _, result := range target.Decoded {
					eligible = eligible || result.Mode == mode && slices.Contains([]api.HDRDecodedState{
						api.HDRDecodedFailed, api.HDRDecodedCanceled, api.HDRDecodedInterrupted, api.HDRDecodedUnsupported, api.HDRDecodedInconclusive,
					}, result.State)
				}
			}
			if !eligible {
				return CommandResult{}, fmt.Errorf("%w: decoded retry must select unsuccessful requested modes", ErrInvalidTransition)
			}
		}
	}
	if err := m.checkpointHDRAttempt(ctx, ownerID, state.Workflow.ID, releaseRef); err != nil {
		return CommandResult{}, err
	}
	measurements := m.hdrMeasurementStore(ctx, ownerID, state, releaseRef, now)
	measurements.RetainedArtifactsOnly = command.WorkflowID == "" && prior != nil && normalized.Decoded == nil &&
		api.IsNativeHDRProfile(prior.ProfileVersion)
	measurements.Comparison, err = m.resolveHDRComparison(ownerID, *state, normalized)
	if err != nil {
		if !errors.Is(err, ErrPrivateResourceUnavailable) || normalized.Decoded == nil || !normalized.Decoded.Automatic ||
			state.OwnerID != ownerID || prior == nil || prior.Release != normalized.Release || !reflect.DeepEqual(prior.Decoded, normalized.Decoded) {
			return CommandResult{}, err
		}
		// Same-generation recovery keeps the original measurement identity.
		// Missing secondary authority never authorizes a new secondary read.
	}
	if command.Comparison != nil && (state.Workflow.HDRComparison == nil || command.Comparison.Ref != *state.Workflow.HDRComparison ||
		measurements.Comparison != nil && command.Comparison.Ref != measurements.Comparison.Ref) {
		return CommandResult{}, ErrPrivateResourceIntegrity
	}
	// Only an explicit analysis request establishes resumable retry authority.
	// Save its boundaries before retiring old evidence or publishing new work.
	var nativeRetryAfterSequence *uint64
	if command.WorkflowID != "" {
		pending := PendingHDRRequest{Instructions: normalized, NativeRetryAfterSequence: new(extractionSequence)}
		if previous := state.HDRPendingRequest; previous != nil && reflect.DeepEqual(previous.Instructions, normalized) {
			pending.RetryAfterSequence = previous.RetryAfterSequence
			if previous.NativeRetryAfterSequence != nil {
				pending.NativeRetryAfterSequence = previous.NativeRetryAfterSequence
			}
		} else {
			for _, record := range state.HDRMeasurements {
				pending.RetryAfterSequence = max(pending.RetryAfterSequence, record.Sequence)
			}
		}
		if err := m.repository.CheckpointHDRRequest(ctx, ownerID, state.Workflow.ID, state.Workflow.Revision, pending); err != nil {
			return CommandResult{}, fmt.Errorf("checkpoint HDR request: %w", err)
		}
		state.HDRPendingRequest = &pending
		nativeRetryAfterSequence = pending.NativeRetryAfterSequence
		extractionSequence = max(extractionSequence, *nativeRetryAfterSequence)
		measurements.RetryAfterSequence = &pending.RetryAfterSequence
		measurements.NativeRetryAfterSequence = nativeRetryAfterSequence
	}
	if prior != nil && prior.Release == releaseRef {
		for id, record := range state.HDRExtractions {
			if !api.IsNativeHDRProfile(normalized.ProfileVersion) && record.Release == releaseRef &&
				slices.Contains(normalized.TargetIDs, record.TargetID) &&
				prior.TargetNeedsSourceRetry(record.TargetID) && (nativeRetryAfterSequence == nil || record.Sequence <= *nativeRetryAfterSequence) {
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
		hdrPreparation(release),
		normalized,
		attemptID,
		now,
		prior,
		priorResource,
		state.HDRExtractions,
		resources,
		publish,
		&measurements,
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
	if command.WorkflowID != "" {
		state.HDRPendingRequest = nil
	}
	state.Workflow.HDRAnalysis = &api.HDRAnalysisRef{ID: snapshot.ID, Revision: snapshot.Revision}
	state.Workflow.HDRAnalysisEnabled = snapshot.CanIncludeDefaultImages()
	if selection := state.Workflow.HDRImages; selection != nil && api.IsNativeHDRProfile(snapshot.ProfileVersion) {
		selected := remapHDRImages(selection, prior, &snapshot)
		state.Workflow.HDRImages = selected
		state.Workflow.HDRAnalysisEnabled = len(selected.ArtifactIDs) > 0
	} else {
		state.Workflow.HDRImages = nil
	}
	state.Workflow.Descriptions = nil
	invalidateUploadPlan(&state.Workflow)
	return CommandResult{HDRAnalysis: &snapshot}, nil
}

func (m *Module) setHDRAnalysisEnabled(ctx context.Context, state *State, command SetHDRAnalysisEnabledCommand) error {
	if len(command.ArtifactIDs) > api.HDRAnalysisMaxTargets*216 || !command.Enabled && len(command.ArtifactIDs) > 0 {
		return fmt.Errorf("%w: HDR image selection is invalid", ErrInvalidTransition)
	}
	if command.Enabled {
		if state.Workflow.HDRAnalysis == nil {
			return fmt.Errorf("%w: generate HDR analysis before enabling inclusion", ErrInvalidTransition)
		}
		analysis, ok := state.HDRAnalyses[state.Workflow.HDRAnalysis.ID]
		if !ok || analysis.Revision != state.Workflow.HDRAnalysis.Revision || !analysis.CanIncludeImages() {
			return fmt.Errorf("%w: every HDR target must complete before inclusion", ErrInvalidTransition)
		}
		if len(command.ArtifactIDs) == 0 && !analysis.CanIncludeDefaultImages() {
			return fmt.Errorf("%w: select decoded HDR images explicitly when no native metadata image is available", ErrInvalidTransition)
		}
		if len(command.ArtifactIDs) > 0 && !api.IsNativeHDRProfile(analysis.ProfileVersion) {
			return fmt.Errorf("%w: per-image selection requires a native HDR analysis", ErrInvalidTransition)
		}
		selected := make(map[api.PublicResourceID]bool, len(command.ArtifactIDs))
		for _, id := range command.ArtifactIDs {
			if id == "" || selected[id] {
				return fmt.Errorf("%w: HDR image selection is invalid", ErrInvalidTransition)
			}
			selected[id] = true
		}
		release, err := currentReleaseSnapshot(state)
		if err != nil {
			return err
		}
		if validator, ok := m.hdrAnalysisBuilder.(HDRAnalysisAuthorityValidator); ok {
			if err := validator.ValidateAuthority(ctx, hdrPreparation(release), analysis); err != nil {
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
			images := target.DescriptionImages()
			if len(command.ArtifactIDs) > 0 {
				images = target.SelectableImages()
			}
			for _, artifact := range images {
				if len(command.ArtifactIDs) > 0 && !selected[artifact.ID] {
					continue
				}
				if _, err := resource.LocalArtifactPath(analysis, artifact.ID); err != nil {
					return fmt.Errorf("hdr_analysis: %w", err)
				}
				delete(selected, artifact.ID)
			}
		}
		if len(selected) > 0 {
			return fmt.Errorf("%w: selection contains unavailable HDR images", ErrInvalidTransition)
		}
	}
	if !command.Enabled && state.Composite != nil {
		state.Composite.Intent.HDRAnalysis = nil
	}
	state.Workflow.HDRAnalysisEnabled = command.Enabled
	state.Workflow.HDRImages = nil
	if !command.Enabled || len(command.ArtifactIDs) > 0 {
		state.Workflow.HDRImages = &api.HDRImageSelection{ArtifactIDs: slices.Clone(command.ArtifactIDs)}
	}
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
		if err := validator.ValidateAuthority(ctx, hdrPreparation(release), analysis); err != nil {
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

// HDRAnalysisArtifact opens an artifact only from the owner's current analysis
// revision and exact prepared generation. The caller closes the returned Body.
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

// HDRAnalysisArtifactPath returns a verified host path owned by the current
// analysis resource. The path is private and does not transfer file ownership.
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

// checkpointHDRAttempt serializes cleanup authority with operation progress before allocation, without advancing workflow intent.
func (m *Module) checkpointHDRAttempt(ctx context.Context, owner string, workflow api.WorkflowID, release api.ReleaseRef) error {
	operationID, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID)
	if operationID == "" {
		return nil
	}
	lock := m.operationLock(operationID)
	lock.Lock()
	defer lock.Unlock()
	record, err := m.operations.LoadOperation(ctx, owner, workflow, operationID)
	if err != nil {
		return fmt.Errorf("load HDR attempt cleanup authority: %w", err)
	}
	if !workflowOperationActive(record.Status.Status) || record.ProcessEpoch != m.processEpoch {
		return ErrOperationConflict
	}
	if record.HDRCleanupRelease == release {
		return nil
	}
	if record.HDRCleanupRelease != (api.ReleaseRef{}) {
		return ErrPrivateResourceIntegrity
	}
	record.HDRCleanupRelease = release
	sequence := record.Status.Sequence
	record.Status.Sequence++
	record.Status.UpdatedAt = m.clock.Now().UTC()
	record.Status.Events = nil
	if err := m.operations.SaveOperation(ctx, sequence, record); err != nil {
		return fmt.Errorf("checkpoint HDR attempt cleanup authority: %w", err)
	}
	return nil
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
	refs := map[api.ReleaseRef]bool{record.HDRCleanupRelease: true}
	for _, snapshot := range state.Releases {
		ref := api.ReleaseRef{SourcePath: snapshot.Release.Source.SourcePath, Generation: snapshot.Release.Generation}
		refs[ref] = true
	}
	for _, entry := range state.HDRExtractions {
		refs[entry.Release] = true
	}
	for ref := range refs {
		if ref.Generation == 0 || ref.SourcePath == "" {
			continue
		}
		if err := cleaner.CleanupAttempt(ref, attempt, state.HDRExtractions, state.HDRMeasurements); err != nil {
			return fmt.Errorf("clean interrupted HDR files: %w", err)
		}
	}
	return nil
}
