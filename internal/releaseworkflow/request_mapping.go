// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// CommandFromRequest validates one shared adapter request and maps it to the
// workflow command consumed by every CLI, browser, and public HTTP entrypoint.
func CommandFromRequest(request any) (Command, error) {
	if validator, ok := request.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return nil, fmt.Errorf("validate release workflow request: %w", err)
		}
	}

	switch request := request.(type) {
	case api.CreateReleaseWorkflowRequest:
		return CreateWorkflowCommand{Instructions: request.Instructions, IdempotencyKey: request.IdempotencyKey}, nil
	case api.ReplaceReleaseWorkflowFactsRequest:
		return ReplaceFactInstructionsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Instructions:     request.Instructions,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.PrepareReleaseWorkflowRequest:
		return PrepareReleaseCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Input:            request.Input,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.ResetReleaseWorkflowRequest:
		return ResetReleaseCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Input:            request.Input,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.SelectReleaseWorkflowCandidateRequest:
		return SelectBlurayCandidateCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			ReleaseID:        request.ReleaseID,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.ProjectReleaseWorkflowTrackersRequest:
		return ProjectTrackersCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			TrackerIDs:       request.TrackerIDs,
			Instructions:     request.Instructions,
			ExecutionMode:    request.ExecutionMode,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.PreflightReleaseWorkflowTrackersRequest:
		return PreflightTrackersCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			InputFingerprint: request.InputFingerprint,
			Interaction:      request.Interaction,
			ExecutionMode:    request.ExecutionMode,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.CheckReleaseWorkflowDuplicatesRequest:
		return CheckDuplicatesCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			SkipRemote:       request.SkipRemote,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.DecideReleaseWorkflowDuplicatesRequest:
		return DecideDuplicatesCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Decisions:        request.Decisions,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.CaptureReleaseWorkflowMediaRequest:
		return CaptureMediaCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Instructions:     request.Instructions,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.SetReleaseWorkflowMediaSelectionRequest:
		return SetMediaSelectionCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			Selected:         request.Selected,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.DeleteReleaseWorkflowMediaRequest:
		return DeleteMediaArtifactsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.ReorderReleaseWorkflowMediaRequest:
		return ReorderMediaArtifactsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.AttachReleaseWorkflowMediaRequest:
		return AttachMediaArtifactsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			Attachments:      request.Attachments,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.UploadReleaseWorkflowImagesRequest:
		return UploadMediaImagesCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			Host:             request.Host,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.RetryReleaseWorkflowImageHostRequest:
		return UploadMediaImagesCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			Host:             request.Host,
			Retry:            true,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.RemoveReleaseWorkflowHostedImagesRequest:
		return RemoveHostedImagesCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Media:            request.Media,
			ArtifactIDs:      request.ArtifactIDs,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.GenerateReleaseWorkflowDescriptionsRequest:
		return GenerateDescriptionsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Instructions:     request.Instructions,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.SaveReleaseWorkflowDescriptionOverrideRequest:
		return SaveDescriptionOverrideCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Descriptions:     request.Override.Descriptions,
			GroupKey:         request.Override.GroupKey,
			Source:           request.Override.Source,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.ResetReleaseWorkflowDescriptionOverrideRequest:
		return ResetDescriptionOverrideCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Descriptions:     request.Descriptions,
			GroupKey:         request.GroupKey,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.DryRunReleaseWorkflowRequest:
		return DryRunUploadsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			NoSeed:           request.NoSeed,
			TrackerIDs:       append([]api.TrackerID(nil), request.TrackerIDs...),
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.UploadReleaseWorkflowRequest:
		return ExecuteUploadsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			NoSeed:           request.NoSeed,
			TrackerIDs:       append([]api.TrackerID(nil), request.TrackerIDs...),
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.RetryReleaseWorkflowUploadRequest:
		return RetryFailedUploadsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Retry:            request.Retry,
			NoSeed:           request.NoSeed,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.RetryReleaseWorkflowClientInjectionRequest:
		return RetryClientInjectionsCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Retry:            request.Retry,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.CancelReleaseWorkflowRequest:
		return CancelWorkflowCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Reason:           request.Reason,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.InvalidateReleaseWorkflowTrackersRequest:
		return InvalidateTrackersCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			TrackerIDs:       request.TrackerIDs,
			Reason:           request.Reason,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	case api.ResolveReleaseWorkflowActionRequest:
		return ResolveActionCommand{
			WorkflowID:       request.WorkflowID,
			ExpectedRevision: request.ExpectedRevision,
			Answer:           request.Answer,
			IdempotencyKey:   request.IdempotencyKey,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported release workflow request %T", request)
	}
}
