// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func (b *Backend) continueReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
) (releaseworkflow.CommandResult, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	result, err := workflowCore.ContinueReleaseWorkflow(ctx, ownerID, request)
	if err != nil {
		return releaseworkflow.CommandResult{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) startReleaseWorkflowUpload(
	ctx context.Context,
	ownerID string,
	request api.CreateReleaseWorkflowUploadRequest,
) (releaseworkflow.CommandResult, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	result, err := workflowCore.StartReleaseWorkflowUpload(ctx, ownerID, request)
	if err != nil {
		return releaseworkflow.CommandResult{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) submitReleaseWorkflowUploadFeedback(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	feedback api.ReleaseWorkflowUploadFeedback,
) (releaseworkflow.CommandResult, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	result, err := workflowCore.SubmitReleaseWorkflowUploadFeedback(ctx, ownerID, workflowID, feedback)
	if err != nil {
		return releaseworkflow.CommandResult{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) executeReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (releaseworkflow.CommandResult, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	result, err := workflowCore.ExecuteReleaseWorkflow(ctx, ownerID, command)
	if err != nil {
		b.logErrorf(
			"releaseworkflow: command=%T state=failed error=%v cause=%s",
			command,
			err,
			releaseWorkflowDiagnosticMessage(err),
		)
		return releaseworkflow.CommandResult{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) startReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (api.WorkflowOperationStatus, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	operation, err := workflowCore.StartReleaseWorkflow(ctx, ownerID, command)
	if err != nil {
		b.logErrorf(
			"releaseworkflow: command=%T state=failed error=%v cause=%s",
			command,
			err,
			releaseWorkflowDiagnosticMessage(err),
		)
		return api.WorkflowOperationStatus{}, classifyReleaseWorkflowError(err)
	}
	return operation, nil
}

// releaseWorkflowDiagnosticMessage extracts the retained private cause from a
// transport-safe operation error, then sanitizes it before it reaches logs.
func releaseWorkflowDiagnosticMessage(err error) string {
	if err == nil {
		return "unknown error"
	}
	diagnostic := err
	var operationError *api.OperationError
	if errors.As(err, &operationError) {
		if cause := errors.Unwrap(operationError); cause != nil {
			diagnostic = cause
		}
	}
	return logging.SanitizeMessage(diagnostic.Error())
}

func (b *Backend) currentReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (releaseworkflow.CommandResult, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.CommandResult{}, err
	}
	result, err := workflowCore.CurrentReleaseWorkflow(ctx, ownerID, workflowID)
	if err != nil {
		return releaseworkflow.CommandResult{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) releaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	result, err := workflowCore.ReleaseWorkflowOperation(ctx, ownerID, workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) cancelReleaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.WorkflowOperationStatus{}, err
	}
	result, err := workflowCore.CancelReleaseWorkflowOperation(ctx, ownerID, workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, classifyReleaseWorkflowError(err)
	}
	return result, nil
}

func (b *Backend) openReleaseWorkflowMediaArtifact(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	media api.MediaArtifactSetRef,
	artifactID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, err
	}
	content, err := workflowCore.OpenReleaseWorkflowMediaArtifact(ctx, ownerID, workflowID, media, artifactID)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, classifyReleaseWorkflowError(err)
	}
	return content, nil
}

func (b *Backend) releaseWorkflowMediaPlan(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.MediaPlan, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.MediaPlan{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.MediaPlan{}, err
	}
	plan, err := workflowCore.ReleaseWorkflowMediaPlan(ctx, ownerID, workflowID)
	if err != nil {
		return api.MediaPlan{}, classifyReleaseWorkflowError(err)
	}
	return plan, nil
}

func (b *Backend) previewReleaseWorkflowFrame(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	timestampSeconds float64,
) (api.FramePreview, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.FramePreview{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.FramePreview{}, err
	}
	preview, err := workflowCore.PreviewReleaseWorkflowFrame(ctx, ownerID, workflowID, expectedRevision, timestampSeconds)
	if err != nil {
		return api.FramePreview{}, classifyReleaseWorkflowError(err)
	}
	return preview, nil
}

func (b *Backend) openReleaseWorkflowPreview(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	previewID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, err
	}
	content, err := workflowCore.OpenReleaseWorkflowPreview(ctx, ownerID, workflowID, previewID)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, classifyReleaseWorkflowError(err)
	}
	return content, nil
}

func (b *Backend) stageReleaseWorkflowMediaResource(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	content releaseworkflow.StagedMediaContent,
) (api.WorkflowResourceRef, error) {
	runtime, err := b.requireRuntime()
	if err != nil {
		return api.WorkflowResourceRef{}, err
	}
	workflowCore, err := runtime.releaseWorkflowCore()
	if err != nil {
		return api.WorkflowResourceRef{}, err
	}
	resource, err := workflowCore.StageReleaseWorkflowMediaResource(ctx, ownerID, workflowID, expectedRevision, content)
	if err != nil {
		return api.WorkflowResourceRef{}, classifyReleaseWorkflowError(err)
	}
	return resource, nil
}

func classifyReleaseWorkflowError(err error) error {
	if err == nil {
		return nil
	}
	var operationError *api.OperationError
	if errors.As(err, &operationError) {
		return operationError
	}
	failure := api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: api.OperationKindUnknown,
		Message:   "The release workflow command could not be completed.",
		Recovery:  api.OperationRecoveryRetry,
	}
	switch {
	case errors.Is(err, releaseworkflow.ErrWorkflowNotFound):
		failure.Code = api.OperationFailureMissingPrerequisite
		failure.Message = "The release workflow is unavailable. Start a new workflow."
		failure.Recovery = api.OperationRecoveryRefreshRelease
	case errors.Is(err, releaseworkflow.ErrRevisionConflict), errors.Is(err, releaseworkflow.ErrIdempotencyConflict),
		errors.Is(err, releaseworkflow.ErrOperationConflict):
		failure.Code = api.OperationFailureStaleReview
		failure.Message = "The release workflow changed. Reload its current state before continuing."
		failure.Recovery = api.OperationRecoveryReviewAgain
	case errors.Is(err, releaseworkflow.ErrInvalidTransition):
		failure.Code = api.OperationFailureMissingPrerequisite
		failure.Message = "Complete the workflow's required action or prerequisite first."
		failure.Recovery = api.OperationRecoveryCompletePrerequisite
	case errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable), errors.Is(err, releaseworkflow.ErrPrivateResourceConsumed):
		failure.Code = api.OperationFailureStaleReview
		failure.Operation = api.OperationKindUploadDryRun
		failure.Message = "The reviewed upload authority is unavailable. Prepare and review the release again."
		failure.Recovery = api.OperationRecoveryReviewAgain
	}
	return api.NewOperationError(failure, err)
}
