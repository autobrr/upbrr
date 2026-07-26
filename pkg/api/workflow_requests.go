// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
)

// ReleaseWorkflowCommandContext binds one user intent to an exact workflow revision.
type ReleaseWorkflowCommandContext struct {
	WorkflowID       WorkflowID       `json:"workflowId"`
	ExpectedRevision WorkflowRevision `json:"expectedRevision"`
	IdempotencyKey   string           `json:"idempotencyKey"`
}

// Validate rejects commands that are not bound to exact workflow authority.
func (c ReleaseWorkflowCommandContext) Validate() error {
	if strings.TrimSpace(string(c.WorkflowID)) == "" {
		return errors.New("workflow ID is required")
	}
	if c.ExpectedRevision == 0 {
		return errors.New("expected workflow revision is required")
	}
	if strings.TrimSpace(c.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	return nil
}

// CreateReleaseWorkflowRequest creates one workflow from fact instructions.
type CreateReleaseWorkflowRequest struct {
	Instructions   ReleaseFactInstructions `json:"instructions"`
	IdempotencyKey string                  `json:"idempotencyKey"`
}

// Validate rejects a create intent without idempotency authority.
func (r CreateReleaseWorkflowRequest) Validate() error {
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	return nil
}

// GetReleaseWorkflowRequest retrieves one owner-scoped workflow.
type GetReleaseWorkflowRequest struct {
	WorkflowID WorkflowID `json:"workflowId"`
}

// Validate rejects an empty workflow lookup.
func (r GetReleaseWorkflowRequest) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" {
		return errors.New("workflow ID is required")
	}
	return nil
}

// ReleaseWorkflowOperationRequest retrieves or cancels one owner-scoped operation.
type ReleaseWorkflowOperationRequest struct {
	WorkflowID  WorkflowID          `json:"workflowId"`
	OperationID WorkflowOperationID `json:"operationId"`
}

// Validate rejects an incomplete workflow-operation ref.
func (r ReleaseWorkflowOperationRequest) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" {
		return errors.New("workflow ID is required")
	}
	if strings.TrimSpace(string(r.OperationID)) == "" {
		return errors.New("workflow operation ID is required")
	}
	return nil
}

// ReplaceReleaseWorkflowFactsRequest replaces fact instructions and invalidates dependent state.
type ReplaceReleaseWorkflowFactsRequest struct {
	ReleaseWorkflowCommandContext
	Instructions ReleaseFactInstructions `json:"instructions"`
}

// Validate verifies exact revision authority.
func (r ReplaceReleaseWorkflowFactsRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// PrepareReleaseWorkflowRequest prepares canonical facts for one workflow.
type PrepareReleaseWorkflowRequest struct {
	ReleaseWorkflowCommandContext
	Input PrepareInput `json:"input"`
}

// Validate verifies authority and required preparation input.
func (r PrepareReleaseWorkflowRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Input.SourcePath) == "" {
		return errors.New("preparation source path is required")
	}
	return nil
}

// ResetReleaseWorkflowRequest explicitly resets fact instructions and re-prepares.
type ResetReleaseWorkflowRequest struct {
	ReleaseWorkflowCommandContext
	Input PrepareInput `json:"input"`
}

// Validate verifies authority and required reset source.
func (r ResetReleaseWorkflowRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Input.SourcePath) == "" {
		return errors.New("reset source path is required")
	}
	return nil
}

// SelectReleaseWorkflowCandidateRequest selects one prepared Blu-ray candidate.
type SelectReleaseWorkflowCandidateRequest struct {
	ReleaseWorkflowCommandContext
	ReleaseID string `json:"releaseId"`
}

// GetReleaseWorkflowMediaPlanRequest retrieves the safe media plan for one workflow.
type GetReleaseWorkflowMediaPlanRequest struct {
	WorkflowID WorkflowID `json:"workflowId"`
}

// Validate rejects an empty workflow lookup.
func (r GetReleaseWorkflowMediaPlanRequest) Validate() error {
	return GetReleaseWorkflowRequest(r).Validate()
}

// PreviewReleaseWorkflowFrameRequest creates a non-authoritative frame preview.
type PreviewReleaseWorkflowFrameRequest struct {
	ReleaseWorkflowCommandContext
	TimestampSeconds float64 `json:"timestampSeconds"`
}

// Validate verifies exact workflow authority and a non-negative timestamp.
func (r PreviewReleaseWorkflowFrameRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if r.TimestampSeconds < 0 {
		return errors.New("frame preview timestamp must not be negative")
	}
	return nil
}

// Validate verifies candidate and revision authority.
func (r SelectReleaseWorkflowCandidateRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ReleaseID) == "" {
		return errors.New("blu-ray release ID is required")
	}
	return nil
}

// ProjectReleaseWorkflowTrackersRequest selects and projects trackers.
type ProjectReleaseWorkflowTrackersRequest struct {
	ReleaseWorkflowCommandContext
	TrackerIDs    []TrackerID                                 `json:"trackerIds"`
	Instructions  map[TrackerID]TrackerProjectionInstructions `json:"instructions,omitempty"`
	ExecutionMode WorkflowExecutionMode                       `json:"executionMode,omitempty"`
}

// Validate verifies exact revision authority.
func (r ProjectReleaseWorkflowTrackersRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if !validWorkflowExecutionMode(r.ExecutionMode) {
		return fmt.Errorf("unsupported workflow execution mode %q", r.ExecutionMode)
	}
	return nil
}

// PreflightReleaseWorkflowTrackersRequest runs live readiness checks.
type PreflightReleaseWorkflowTrackersRequest struct {
	ReleaseWorkflowCommandContext
	InputFingerprint WorkflowFingerprint   `json:"inputFingerprint,omitempty"`
	Interaction      InteractionMode       `json:"interaction,omitempty"`
	ExecutionMode    WorkflowExecutionMode `json:"executionMode,omitempty"`
}

// Validate verifies exact revision authority.
func (r PreflightReleaseWorkflowTrackersRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if !validWorkflowInteraction(r.Interaction) {
		return fmt.Errorf("unsupported workflow interaction mode %q", r.Interaction)
	}
	if !validWorkflowExecutionMode(r.ExecutionMode) {
		return fmt.Errorf("unsupported workflow execution mode %q", r.ExecutionMode)
	}
	return nil
}

// CheckReleaseWorkflowDuplicatesRequest runs projection-bound duplicate checks.
type CheckReleaseWorkflowDuplicatesRequest struct {
	ReleaseWorkflowCommandContext
	SkipRemote bool `json:"skipRemote"`
}

// Validate verifies exact revision authority.
func (r CheckReleaseWorkflowDuplicatesRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// DecideReleaseWorkflowDuplicatesRequest records owner duplicate decisions.
type DecideReleaseWorkflowDuplicatesRequest struct {
	ReleaseWorkflowCommandContext
	Decisions map[TrackerID]DupeDecision `json:"decisions"`
}

// Validate verifies exact revision authority.
func (r DecideReleaseWorkflowDuplicatesRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// CaptureReleaseWorkflowMediaRequest captures authoritative media.
type CaptureReleaseWorkflowMediaRequest struct {
	ReleaseWorkflowCommandContext
	Instructions MediaCaptureInstructions `json:"instructions"`
}

// Validate verifies exact revision authority.
func (r CaptureReleaseWorkflowMediaRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// SetReleaseWorkflowMediaSelectionRequest changes opaque artifact selection.
type SetReleaseWorkflowMediaSelectionRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
	Selected    bool                `json:"selected"`
}

// Validate verifies revision authority and opaque media refs.
func (r SetReleaseWorkflowMediaSelectionRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateWorkflowMediaMutation(r.Media, r.ArtifactIDs)
}

// DeleteReleaseWorkflowMediaRequest deletes opaque retained artifacts.
type DeleteReleaseWorkflowMediaRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
}

// Validate verifies revision authority and opaque media refs.
func (r DeleteReleaseWorkflowMediaRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateWorkflowMediaMutation(r.Media, r.ArtifactIDs)
}

// ReorderReleaseWorkflowMediaRequest establishes exact artifact order.
type ReorderReleaseWorkflowMediaRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
}

// Validate verifies exact revision authority and ordered opaque media refs.
func (r ReorderReleaseWorkflowMediaRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateWorkflowMediaMutation(r.Media, r.ArtifactIDs)
}

// AttachReleaseWorkflowMediaRequest attaches staged resources to exact media lineage.
type AttachReleaseWorkflowMediaRequest struct {
	ReleaseWorkflowCommandContext
	Media       *MediaArtifactSetRef `json:"media,omitempty"`
	Attachments []MediaAttachment    `json:"attachments"`
}

// Validate verifies exact workflow authority and opaque staged-resource refs.
func (r AttachReleaseWorkflowMediaRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if len(r.Attachments) == 0 {
		return errors.New("at least one media attachment is required")
	}
	for _, attachment := range r.Attachments {
		if strings.TrimSpace(string(attachment.Resource.ID)) == "" {
			return errors.New("staged media resource ID is required")
		}
	}
	return nil
}

// UploadReleaseWorkflowImagesRequest hosts selected artifacts for exact media lineage.
type UploadReleaseWorkflowImagesRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds,omitempty"`
	Host        string              `json:"host,omitempty"`
}

// Validate verifies exact media and optional compatibility add-host intent.
// Empty artifact IDs mean every currently selected local artifact. Empty host
// means backend-derived required host planning.
func (r UploadReleaseWorkflowImagesRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if err := validateWorkflowMediaRef(r.Media); err != nil {
		return err
	}
	for _, artifactID := range r.ArtifactIDs {
		if strings.TrimSpace(string(artifactID)) == "" {
			return errors.New("media artifact ID is required")
		}
	}
	return nil
}

// RemoveReleaseWorkflowHostedImagesRequest removes hosted outcomes by opaque ID.
type RemoveReleaseWorkflowHostedImagesRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
}

// Validate verifies exact media and hosted artifact refs.
func (r RemoveReleaseWorkflowHostedImagesRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateWorkflowMediaMutation(r.Media, r.ArtifactIDs)
}

// RetryReleaseWorkflowImageHostRequest explicitly retries one prior failed host.
type RetryReleaseWorkflowImageHostRequest struct {
	ReleaseWorkflowCommandContext
	Media       MediaArtifactSetRef `json:"media"`
	ArtifactIDs []PublicResourceID  `json:"artifactIds"`
	Host        string              `json:"host"`
}

// Validate verifies exact media and explicit host retry intent.
func (r RetryReleaseWorkflowImageHostRequest) Validate() error {
	if err := UploadReleaseWorkflowImagesRequest(r).Validate(); err != nil {
		return err
	}
	if len(r.ArtifactIDs) == 0 {
		return errors.New("at least one media artifact ID is required")
	}
	if strings.TrimSpace(r.Host) == "" {
		return errors.New("image host is required")
	}
	return nil
}

func validateWorkflowMediaMutation(media MediaArtifactSetRef, artifactIDs []PublicResourceID) error {
	if err := validateWorkflowMediaRef(media); err != nil {
		return err
	}
	if len(artifactIDs) == 0 {
		return errors.New("at least one media artifact ID is required")
	}
	for _, artifactID := range artifactIDs {
		if strings.TrimSpace(string(artifactID)) == "" {
			return errors.New("media artifact ID is required")
		}
	}
	return nil
}

func validateWorkflowMediaRef(media MediaArtifactSetRef) error {
	if strings.TrimSpace(string(media.ID)) == "" || media.Revision == 0 {
		return errors.New("exact media artifact set is required")
	}
	return nil
}

// GenerateReleaseWorkflowDescriptionsRequest generates authoritative descriptions.
type GenerateReleaseWorkflowDescriptionsRequest struct {
	ReleaseWorkflowCommandContext
	Instructions DescriptionInstructions `json:"instructions"`
}

// Validate verifies exact revision authority.
func (r GenerateReleaseWorkflowDescriptionsRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// SaveReleaseWorkflowDescriptionOverrideRequest saves one exact description group.
type SaveReleaseWorkflowDescriptionOverrideRequest struct {
	ReleaseWorkflowCommandContext
	Override DescriptionOverrideMutation `json:"override"`
}

// Validate verifies exact description lineage and one non-empty group key.
func (r SaveReleaseWorkflowDescriptionOverrideRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(r.Override.Descriptions.ID)) == "" || r.Override.Descriptions.Revision == 0 {
		return errors.New("exact description set is required")
	}
	if strings.TrimSpace(r.Override.GroupKey) == "" {
		return errors.New("description group key is required")
	}
	return nil
}

// ResetReleaseWorkflowDescriptionOverrideRequest resets one exact description group.
type ResetReleaseWorkflowDescriptionOverrideRequest struct {
	ReleaseWorkflowCommandContext
	Descriptions DescriptionSetRef `json:"descriptions"`
	GroupKey     string            `json:"groupKey"`
}

// Validate verifies exact description lineage and one non-empty group key.
func (r ResetReleaseWorkflowDescriptionOverrideRequest) Validate() error {
	return SaveReleaseWorkflowDescriptionOverrideRequest{
		ReleaseWorkflowCommandContext: r.ReleaseWorkflowCommandContext,
		Override: DescriptionOverrideMutation{
			Descriptions: r.Descriptions,
			GroupKey:     r.GroupKey,
		},
	}.Validate()
}

// DryRunReleaseWorkflowRequest prepares safe reports without tracker submission.
type DryRunReleaseWorkflowRequest struct {
	ReleaseWorkflowCommandContext
	NoSeed     bool        `json:"noSeed"`
	TrackerIDs []TrackerID `json:"trackerIds,omitempty"`
}

// Validate verifies exact workflow authority.
func (r DryRunReleaseWorkflowRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateOptionalWorkflowTrackerIDs(r.TrackerIDs)
}

// UploadReleaseWorkflowRequest directly prepares and executes eligible trackers.
type UploadReleaseWorkflowRequest struct {
	ReleaseWorkflowCommandContext
	NoSeed     bool        `json:"noSeed"`
	TrackerIDs []TrackerID `json:"trackerIds,omitempty"`
}

// Validate verifies exact workflow authority.
func (r UploadReleaseWorkflowRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	return validateOptionalWorkflowTrackerIDs(r.TrackerIDs)
}

func validateOptionalWorkflowTrackerIDs(trackerIDs []TrackerID) error {
	seen := make(map[TrackerID]struct{}, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		trackerID = TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if trackerID == "" {
			return errors.New("tracker ID is required")
		}
		if _, duplicate := seen[trackerID]; duplicate {
			return fmt.Errorf("tracker %s appears more than once", trackerID)
		}
		seen[trackerID] = struct{}{}
	}
	return nil
}

// RetryReleaseWorkflowUploadRequest retries failed trackers from an exact prior result.
type RetryReleaseWorkflowUploadRequest struct {
	ReleaseWorkflowCommandContext
	Retry  FailedTrackerRetryRef `json:"retry"`
	NoSeed bool                  `json:"noSeed"`
}

// Validate verifies exact workflow authority and explicit failed-tracker targets.
func (r RetryReleaseWorkflowUploadRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(r.Retry.Result.ID)) == "" || r.Retry.Result.Revision == 0 {
		return errors.New("exact prior upload result is required")
	}
	if len(r.Retry.TrackerIDs) == 0 {
		return errors.New("at least one failed tracker ID is required")
	}
	return nil
}

// RetryReleaseWorkflowClientInjectionRequest retries only failed client
// injection effects from an exact prior upload result.
type RetryReleaseWorkflowClientInjectionRequest struct {
	ReleaseWorkflowCommandContext
	Retry ClientInjectionRetryRef `json:"retry"`
}

// Validate verifies exact workflow authority and explicit client-effect targets.
func (r RetryReleaseWorkflowClientInjectionRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(r.Retry.Result.ID)) == "" || r.Retry.Result.Revision == 0 {
		return errors.New("exact prior upload result is required")
	}
	if len(r.Retry.TrackerIDs) == 0 {
		return errors.New("at least one failed client injection tracker ID is required")
	}
	return validateOptionalWorkflowTrackerIDs(r.Retry.TrackerIDs)
}

// CancelReleaseWorkflowRequest cancels one workflow.
type CancelReleaseWorkflowRequest struct {
	ReleaseWorkflowCommandContext
	Reason string `json:"reason,omitempty"`
}

// Validate verifies exact revision authority.
func (r CancelReleaseWorkflowRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}

// InvalidateReleaseWorkflowTrackersRequest invalidates selected tracker chains.
type InvalidateReleaseWorkflowTrackersRequest struct {
	ReleaseWorkflowCommandContext
	TrackerIDs []TrackerID `json:"trackerIds"`
	Reason     string      `json:"reason"`
}

// Validate verifies exact revision authority and a non-empty tracker set.
func (r InvalidateReleaseWorkflowTrackersRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if len(r.TrackerIDs) == 0 {
		return errors.New("at least one tracker ID is required")
	}
	return nil
}

// ResolveReleaseWorkflowActionRequest resolves one retained required action.
type ResolveReleaseWorkflowActionRequest struct {
	ReleaseWorkflowCommandContext
	Answer RequiredActionAnswer `json:"answer"`
}

// Validate verifies exact revision authority and action lineage.
func (r ResolveReleaseWorkflowActionRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(r.Answer.ActionID)) == "" {
		return errors.New("required action ID is required")
	}
	if r.Answer.WorkflowRevision != r.ExpectedRevision {
		return fmt.Errorf("required action revision %d does not match expected revision %d", r.Answer.WorkflowRevision, r.ExpectedRevision)
	}
	return nil
}
