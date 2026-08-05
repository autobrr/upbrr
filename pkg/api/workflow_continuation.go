// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OperationLifecycle describes execution independently from business outcome.
type OperationLifecycle string

const (
	OperationLifecycleQueued   OperationLifecycle = "queued"
	OperationLifecycleRunning  OperationLifecycle = "running"
	OperationLifecycleReady    OperationLifecycle = "ready"
	OperationLifecycleWaiting  OperationLifecycle = "waiting"
	OperationLifecycleTerminal OperationLifecycle = "terminal"
)

// WorkflowDisposition is the reduced business outcome across viable tracker lanes.
type WorkflowDisposition string

const (
	WorkflowDispositionNone        WorkflowDisposition = "none"
	WorkflowDispositionSucceeded   WorkflowDisposition = "succeeded"
	WorkflowDispositionPartial     WorkflowDisposition = "partial"
	WorkflowDispositionFailed      WorkflowDisposition = "failed"
	WorkflowDispositionCanceled    WorkflowDisposition = "canceled"
	WorkflowDispositionNeedsAction WorkflowDisposition = "needs_action"
)

// WorkflowExecutionMode controls policy-gate execution without changing
// dry-run or client-injection semantics.
type WorkflowExecutionMode string

const (
	WorkflowExecutionModeNormal WorkflowExecutionMode = "normal"
	WorkflowExecutionModeDebug  WorkflowExecutionMode = "debug"
)

// NormalizeWorkflowExecutionMode maps the wire-compatible empty value to normal.
func NormalizeWorkflowExecutionMode(mode WorkflowExecutionMode) WorkflowExecutionMode {
	if mode == "" {
		return WorkflowExecutionModeNormal
	}
	return mode
}

// WorkflowGoal is an adapter-facing desired workflow milestone.
type WorkflowGoal string

const (
	WorkflowGoalPrepared          WorkflowGoal = "prepared"
	WorkflowGoalTrackersAssessed  WorkflowGoal = "trackers_assessed"
	WorkflowGoalDuplicatesDecided WorkflowGoal = "duplicates_decided"
	WorkflowGoalMediaReady        WorkflowGoal = "media_ready"
	WorkflowGoalDescriptionsReady WorkflowGoal = "descriptions_ready"
	WorkflowGoalUploadReviewed    WorkflowGoal = "upload_reviewed"
	WorkflowGoalDryRun            WorkflowGoal = "dry_run"
	WorkflowGoalUploaded          WorkflowGoal = "uploaded"
)

// WorkflowAuthority is exact optimistic concurrency authority for Continue.
type WorkflowAuthority struct {
	WorkflowID       WorkflowID       `json:"workflowId"`
	ExpectedRevision WorkflowRevision `json:"expectedRevision"`
}

// WorkflowIntent is desired workflow state. Internal stage ordering is absent by design.
type WorkflowIntent struct {
	FactInstructions       *ReleaseFactInstructions                    `json:"factInstructions,omitempty"`
	Preparation            *PrepareInput                               `json:"preparation,omitempty"`
	Interaction            InteractionMode                             `json:"interaction,omitempty"`
	ExecutionMode          WorkflowExecutionMode                       `json:"executionMode,omitempty"`
	TrackerIDs             []TrackerID                                 `json:"trackerIds,omitempty"`
	ProjectionInstructions map[TrackerID]TrackerProjectionInstructions `json:"projectionInstructions,omitempty"`
	SkipRemoteDuplicates   bool                                        `json:"skipRemoteDuplicates,omitempty"`
	DuplicateCheckCount    uint8                                       `json:"duplicateCheckCount,omitempty"`
	DuplicateDecisions     map[TrackerID]DupeDecision                  `json:"duplicateDecisions,omitempty"`
	Media                  *MediaCaptureInstructions                   `json:"media,omitempty"`
	MediaSelection         *WorkflowMediaSelection                     `json:"mediaSelection,omitempty"`
	Descriptions           *DescriptionInstructions                    `json:"descriptions,omitempty"`
	UploadTrackerIDs       []TrackerID                                 `json:"uploadTrackerIds,omitempty"`
	NoSeed                 bool                                        `json:"noSeed,omitempty"`
}

// WorkflowMediaSelection distinguishes default-all selection from an explicit exact selection.
type WorkflowMediaSelection struct {
	ArtifactIDs []PublicResourceID `json:"artifactIds"`
}

// UploadApproval binds submission permission to one exact reviewed dry run.
//
// Deprecated: final upload approval is no longer required by runtime workflows.
type UploadApproval struct {
	ActionID         RequiredActionID      `json:"actionId"`
	DryRun           UploadDryRunResultRef `json:"dryRun"`
	InputFingerprint WorkflowFingerprint   `json:"inputFingerprint"`
}

// TrackerApproval binds one explicit non-empty tracker subset to exact post-dupe evidence.
type TrackerApproval struct {
	ActionID         RequiredActionID    `json:"actionId"`
	Dupes            DupeAssessmentRef   `json:"dupes"`
	InputFingerprint WorkflowFingerprint `json:"inputFingerprint"`
	TrackerIDs       []TrackerID         `json:"trackerIds"`
}

// ContinueReleaseWorkflowRequest reconciles typed desired state toward one goal.
type ContinueReleaseWorkflowRequest struct {
	Authority       *WorkflowAuthority     `json:"authority,omitempty"`
	IdempotencyKey  string                 `json:"idempotencyKey"`
	Goal            WorkflowGoal           `json:"goal"`
	Intent          WorkflowIntent         `json:"intent"`
	Answers         []RequiredActionAnswer `json:"answers,omitempty"`
	TrackerApproval *TrackerApproval       `json:"trackerApproval,omitempty"`
	// Deprecated: final upload approval is no longer required by runtime workflows.
	Approval *UploadApproval `json:"approval,omitempty"`
}

// Validate verifies request identity and typed desired-state shape.
func (r ContinueReleaseWorkflowRequest) Validate() error {
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	if !validWorkflowGoal(r.Goal) {
		return fmt.Errorf("unsupported workflow goal %q", r.Goal)
	}
	interaction := r.Intent.Interaction
	if interaction == "" && r.Intent.Preparation != nil {
		interaction = r.Intent.Preparation.Controls.Interaction
	}
	if !validWorkflowInteraction(interaction) {
		return fmt.Errorf("unsupported workflow interaction mode %q", interaction)
	}
	if !validWorkflowExecutionMode(r.Intent.ExecutionMode) {
		return fmt.Errorf("unsupported workflow execution mode %q", r.Intent.ExecutionMode)
	}
	if r.Authority == nil {
		if r.Intent.FactInstructions == nil && r.Intent.Preparation == nil {
			return errors.New("begin workflow requires fact or preparation instructions")
		}
	} else if strings.TrimSpace(string(r.Authority.WorkflowID)) == "" || r.Authority.ExpectedRevision == 0 {
		return errors.New("exact workflow authority is required")
	}
	for _, trackerID := range r.Intent.TrackerIDs {
		if strings.TrimSpace(string(trackerID)) == "" {
			return errors.New("tracker ID is required")
		}
	}
	if err := validateOptionalWorkflowTrackerIDs(r.Intent.UploadTrackerIDs); err != nil {
		return fmt.Errorf("upload trackers: %w", err)
	}
	if r.Intent.DuplicateCheckCount > 2 {
		return errors.New("duplicate check count cannot exceed two")
	}
	if r.Intent.MediaSelection != nil {
		if len(r.Intent.MediaSelection.ArtifactIDs) == 0 {
			return errors.New("explicit media selection requires at least one artifact")
		}
		for _, artifactID := range r.Intent.MediaSelection.ArtifactIDs {
			if strings.TrimSpace(string(artifactID)) == "" {
				return errors.New("media artifact ID is required")
			}
		}
	}
	if r.Intent.Descriptions != nil {
		if err := r.Intent.Descriptions.Validate(); err != nil {
			return fmt.Errorf("description intent: %w", err)
		}
	}
	if r.Approval != nil {
		if strings.TrimSpace(string(r.Approval.ActionID)) == "" {
			return errors.New("upload approval action ID is required")
		}
		if strings.TrimSpace(string(r.Approval.DryRun.ID)) == "" || r.Approval.DryRun.Revision == 0 {
			return errors.New("upload approval requires an exact dry run")
		}
		if err := validateWorkflowFingerprint(r.Approval.InputFingerprint); err != nil {
			return fmt.Errorf("upload approval: %w", err)
		}
	}
	if r.TrackerApproval != nil {
		if strings.TrimSpace(string(r.TrackerApproval.ActionID)) == "" {
			return errors.New("tracker approval action ID is required")
		}
		if strings.TrimSpace(string(r.TrackerApproval.Dupes.ID)) == "" || r.TrackerApproval.Dupes.Revision == 0 {
			return errors.New("tracker approval requires an exact duplicate assessment")
		}
		if err := validateWorkflowFingerprint(r.TrackerApproval.InputFingerprint); err != nil {
			return fmt.Errorf("tracker approval: %w", err)
		}
		if len(r.TrackerApproval.TrackerIDs) == 0 {
			return errors.New("tracker approval requires at least one tracker ID")
		}
		seen := make(map[TrackerID]struct{}, len(r.TrackerApproval.TrackerIDs))
		for _, trackerID := range r.TrackerApproval.TrackerIDs {
			normalized := TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
			if normalized == "" {
				return errors.New("tracker approval tracker ID is required")
			}
			if _, duplicate := seen[normalized]; duplicate {
				return fmt.Errorf("tracker approval tracker %s appears more than once", normalized)
			}
			seen[normalized] = struct{}{}
		}
	}
	return nil
}

func validWorkflowInteraction(interaction InteractionMode) bool {
	switch interaction {
	case "", InteractionModeInteractive, InteractionModeUnattended, InteractionModeUnattendedConfirm:
		return true
	default:
		return false
	}
}

func validWorkflowExecutionMode(mode WorkflowExecutionMode) bool {
	switch mode {
	case "", WorkflowExecutionModeNormal, WorkflowExecutionModeDebug:
		return true
	default:
		return false
	}
}

func validWorkflowGoal(goal WorkflowGoal) bool {
	switch goal {
	case WorkflowGoalPrepared,
		WorkflowGoalTrackersAssessed,
		WorkflowGoalDuplicatesDecided,
		WorkflowGoalMediaReady,
		WorkflowGoalDescriptionsReady,
		WorkflowGoalUploadReviewed,
		WorkflowGoalDryRun,
		WorkflowGoalUploaded:
		return true
	default:
		return false
	}
}

// GoalAvailability reports backend-owned transition policy for one goal.
type GoalAvailability struct {
	Goal       WorkflowGoal `json:"goal"`
	Available  bool         `json:"available"`
	ReasonCode string       `json:"reasonCode,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

// WorkflowExactRefs binds a lane projection to retained immutable products.
type WorkflowExactRefs struct {
	Release         *ReleaseSnapshotRef             `json:"release,omitempty"`
	Projections     *TrackerReleaseProjectionSetRef `json:"projections,omitempty"`
	Preflight       *TrackerPreflightAssessmentRef  `json:"preflight,omitempty"`
	Dupes           *DupeAssessmentRef              `json:"dupes,omitempty"`
	TrackerApproval *TrackerApprovalSnapshotRef     `json:"trackerApproval,omitempty"`
	Media           *MediaArtifactSetRef            `json:"media,omitempty"`
	Descriptions    *DescriptionSetRef              `json:"descriptions,omitempty"`
	DryRun          *UploadDryRunResultRef          `json:"dryRun,omitempty"`
	UploadResult    *UploadResultRef                `json:"uploadResult,omitempty"`
}

// TrackerLaneOutcome is the canonical current result for one selected tracker.
type TrackerLaneOutcome struct {
	TrackerID       TrackerID           `json:"trackerId"`
	DisplayName     string              `json:"displayName,omitempty"`
	Goal            WorkflowGoal        `json:"goal,omitempty"`
	Lifecycle       OperationLifecycle  `json:"lifecycle"`
	Disposition     WorkflowDisposition `json:"disposition"`
	Refs            WorkflowExactRefs   `json:"refs"`
	RequiredActions []RequiredAction    `json:"requiredActions,omitempty"`
	Failures        []WorkflowFailure   `json:"failures,omitempty"`
	Retryable       bool                `json:"retryable"`
}

// WorkflowEventScope identifies the authority represented by one safe event.
type WorkflowEventScope string

const (
	WorkflowEventScopeWorkflow WorkflowEventScope = "workflow"
	WorkflowEventScopeTracker  WorkflowEventScope = "tracker"
	WorkflowEventScopeArtifact WorkflowEventScope = "artifact"
	WorkflowEventScopeHost     WorkflowEventScope = "host"
	WorkflowEventScopeClient   WorkflowEventScope = "client"
)

// WorkflowEventSeverity is assigned centrally and rendered unchanged by adapters.
type WorkflowEventSeverity string

const (
	WorkflowEventSeverityDebug WorkflowEventSeverity = "debug"
	WorkflowEventSeverityInfo  WorkflowEventSeverity = "info"
	WorkflowEventSeverityWarn  WorkflowEventSeverity = "warn"
	WorkflowEventSeverityError WorkflowEventSeverity = "error"
)

// WorkflowEvent is one canonical redacted operation or scoped-item event.
type WorkflowEvent struct {
	Sequence    uint64                `json:"sequence"`
	WorkflowID  WorkflowID            `json:"workflowId"`
	OperationID WorkflowOperationID   `json:"operationId,omitempty"`
	Command     string                `json:"command,omitempty"`
	Phase       string                `json:"phase,omitempty"`
	Scope       WorkflowEventScope    `json:"scope"`
	ScopeID     string                `json:"scopeId,omitempty"`
	Lifecycle   OperationLifecycle    `json:"lifecycle"`
	State       StageStatus           `json:"state"`
	Disposition WorkflowDisposition   `json:"disposition"`
	Severity    WorkflowEventSeverity `json:"severity"`
	Completed   int                   `json:"completed,omitempty"`
	Total       int                   `json:"total,omitempty"`
	FailureCode OperationFailureCode  `json:"failureCode,omitempty"`
	Recovery    OperationRecovery     `json:"recovery,omitempty"`
	Message     string                `json:"message,omitempty"`
	Timestamp   time.Time             `json:"timestamp" ts_type:"string"`
}

// WorkflowContinuation is the backend-owned current transition and outcome projection.
type WorkflowContinuation struct {
	Lifecycle       OperationLifecycle   `json:"lifecycle"`
	Disposition     WorkflowDisposition  `json:"disposition"`
	Refs            WorkflowExactRefs    `json:"refs"`
	TrackerOutcomes []TrackerLaneOutcome `json:"trackerOutcomes,omitempty"`
	RequiredActions []RequiredAction     `json:"requiredActions,omitempty"`
	AvailableGoals  []GoalAvailability   `json:"availableGoals"`
	Events          []WorkflowEvent      `json:"events,omitempty"`
}
