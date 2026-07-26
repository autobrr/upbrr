// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WorkflowID identifies one retained release workflow.
type WorkflowID string

// WorkflowRevision is the optimistic concurrency revision of a workflow or stage snapshot.
type WorkflowRevision uint64

// WorkflowFingerprint is a deterministic SHA-256 fingerprint of normalized public inputs.
type WorkflowFingerprint string

// TrackerID is the stable tracker identity used by workflow contracts.
type TrackerID string

// ReleaseFactInstructionSnapshotID identifies one immutable fact-instruction snapshot.
type ReleaseFactInstructionSnapshotID string

// ReleaseSnapshotID identifies one immutable prepared-release projection.
type ReleaseSnapshotID string

// TrackerCatalogSnapshotID identifies one immutable tracker catalog.
type TrackerCatalogSnapshotID string

// TrackerRuntimeSnapshotID identifies one immutable safe runtime snapshot.
type TrackerRuntimeSnapshotID string

// TrackerSelectionID identifies one immutable ordered tracker selection.
type TrackerSelectionID string

// TrackerProjectionInstructionSnapshotID identifies tracker-scoped projection instructions.
type TrackerProjectionInstructionSnapshotID string

// TrackerReleaseProjectionSetID identifies one immutable set of tracker projections.
type TrackerReleaseProjectionSetID string

// TrackerPreflightAssessmentID identifies one immutable live preflight assessment.
type TrackerPreflightAssessmentID string

// DupeAssessmentID identifies one immutable duplicate assessment.
type DupeAssessmentID string

// TrackerApprovalSnapshotID identifies one immutable post-dupe tracker approval.
type TrackerApprovalSnapshotID string

// MediaArtifactSetID identifies one immutable media-artifact set.
type MediaArtifactSetID string

// MediaPlanID identifies one immutable workflow media plan.
type MediaPlanID string

// WorkflowResourceID identifies one owner-scoped staged private resource.
type WorkflowResourceID string

// UploadDryRunResultID identifies one immutable direct dry-run result.
type UploadDryRunResultID string

// DescriptionSetID identifies one immutable generated-description set.
type DescriptionSetID string

// UploadPlanID identifies one retained upload plan's safe public projection.
type UploadPlanID string

// UploadResultID identifies one immutable upload execution result.
type UploadResultID string

// WorkflowOperationID identifies one pollable workflow operation.
type WorkflowOperationID string

// RequiredActionID identifies one typed action required to advance a workflow.
type RequiredActionID string

// PublicResourceID identifies an opaque public artifact without exposing a local path.
type PublicResourceID string

// ReleaseFactInstructionSnapshotRef references one exact instruction snapshot revision.
type ReleaseFactInstructionSnapshotRef struct {
	ID       ReleaseFactInstructionSnapshotID `json:"id"`
	Revision WorkflowRevision                 `json:"revision"`
}

// ReleaseSnapshotRef references one exact prepared-release snapshot revision.
type ReleaseSnapshotRef struct {
	ID       ReleaseSnapshotID `json:"id"`
	Revision WorkflowRevision  `json:"revision"`
}

// TrackerCatalogSnapshotRef references one exact tracker catalog revision.
type TrackerCatalogSnapshotRef struct {
	ID       TrackerCatalogSnapshotID `json:"id"`
	Revision WorkflowRevision         `json:"revision"`
}

// TrackerRuntimeSnapshotRef references one exact tracker runtime revision.
type TrackerRuntimeSnapshotRef struct {
	ID       TrackerRuntimeSnapshotID `json:"id"`
	Revision WorkflowRevision         `json:"revision"`
}

// TrackerSelectionRef references one exact tracker selection revision.
type TrackerSelectionRef struct {
	ID       TrackerSelectionID `json:"id"`
	Revision WorkflowRevision   `json:"revision"`
}

// TrackerProjectionInstructionSnapshotRef references exact tracker projection instructions.
type TrackerProjectionInstructionSnapshotRef struct {
	ID       TrackerProjectionInstructionSnapshotID `json:"id"`
	Revision WorkflowRevision                       `json:"revision"`
}

// TrackerReleaseProjectionSetRef references one exact tracker projection-set revision.
type TrackerReleaseProjectionSetRef struct {
	ID       TrackerReleaseProjectionSetID `json:"id"`
	Revision WorkflowRevision              `json:"revision"`
}

// TrackerPreflightAssessmentRef references one exact preflight assessment revision.
type TrackerPreflightAssessmentRef struct {
	ID       TrackerPreflightAssessmentID `json:"id"`
	Revision WorkflowRevision             `json:"revision"`
}

// DupeAssessmentRef references one exact duplicate assessment revision.
type DupeAssessmentRef struct {
	ID       DupeAssessmentID `json:"id"`
	Revision WorkflowRevision `json:"revision"`
}

// TrackerApprovalSnapshotRef references one exact post-dupe tracker approval.
type TrackerApprovalSnapshotRef struct {
	ID       TrackerApprovalSnapshotID `json:"id"`
	Revision WorkflowRevision          `json:"revision"`
}

// MediaArtifactSetRef references one exact media-artifact revision.
type MediaArtifactSetRef struct {
	ID       MediaArtifactSetID `json:"id"`
	Revision WorkflowRevision   `json:"revision"`
}

// MediaPlanRef references one exact immutable media plan.
type MediaPlanRef struct {
	ID       MediaPlanID      `json:"id"`
	Revision WorkflowRevision `json:"revision"`
}

// UploadDryRunResultRef references one exact dry-run result.
type UploadDryRunResultRef struct {
	ID       UploadDryRunResultID `json:"id"`
	Revision WorkflowRevision     `json:"revision"`
}

// DescriptionSetRef references one exact description-set revision.
type DescriptionSetRef struct {
	ID       DescriptionSetID `json:"id"`
	Revision WorkflowRevision `json:"revision"`
}

// UploadResultRef references one exact upload-result revision.
type UploadResultRef struct {
	ID       UploadResultID   `json:"id"`
	Revision WorkflowRevision `json:"revision"`
}

// WorkflowStatus is the aggregate lifecycle status visible to all adapters.
type WorkflowStatus string

const (
	// WorkflowStatusDraft means the workflow can accept fact instructions.
	WorkflowStatusDraft WorkflowStatus = "draft"
	// WorkflowStatusActive means one or more workflow stages are ready or running.
	WorkflowStatusActive WorkflowStatus = "active"
	// WorkflowStatusBlocked means typed caller action is required.
	WorkflowStatusBlocked WorkflowStatus = "blocked"
	// WorkflowStatusCompleted means upload execution reached a terminal result.
	WorkflowStatusCompleted WorkflowStatus = "completed"
	// WorkflowStatusCanceled means the owner canceled the workflow.
	WorkflowStatusCanceled WorkflowStatus = "canceled"
	// WorkflowStatusFailed means a non-recoverable workflow failure is retained.
	WorkflowStatusFailed WorkflowStatus = "failed"
)

// StageStatus is the lifecycle status shared by immutable workflow stage snapshots.
type StageStatus string

const (
	// StageStatusPending means dependencies are incomplete.
	StageStatusPending StageStatus = "pending"
	// StageStatusQueued means an accepted operation is waiting to run.
	StageStatusQueued StageStatus = "queued"
	// StageStatusReady means the snapshot is valid for its next consumer.
	StageStatusReady StageStatus = "ready"
	// StageStatusBlocked means typed caller action is required.
	StageStatusBlocked StageStatus = "blocked"
	// StageStatusStale means a dependency changed or freshness expired.
	StageStatusStale StageStatus = "stale"
	// StageStatusFailed means the stage reached a retained failure.
	StageStatusFailed StageStatus = "failed"
	// StageStatusPartial means terminal work produced both successes and failures.
	StageStatusPartial StageStatus = "partial"
	// StageStatusSkipped means backend policy determined the stage was unnecessary.
	StageStatusSkipped StageStatus = "skipped"
	// StageStatusRunning means an asynchronous operation is active.
	StageStatusRunning StageStatus = "running"
	// StageStatusCompleted means a non-execution stage completed successfully.
	StageStatusCompleted StageStatus = "completed"
	// StageStatusExecuted means a retained upload plan was consumed.
	StageStatusExecuted StageStatus = "executed"
	// StageStatusInterrupted means a process restart interrupted in-flight work.
	StageStatusInterrupted StageStatus = "interrupted"
	// StageStatusCanceled means the owner canceled an accepted operation.
	StageStatusCanceled StageStatus = "canceled"
	// StageStatusUnavailable means private restart-sensitive resources no longer exist.
	StageStatusUnavailable StageStatus = "unavailable"
)

// ReadinessStatus distinguishes deterministic projection and live readiness outcomes.
type ReadinessStatus string

const (
	// ReadinessStatusUnknown means readiness has not been assessed.
	ReadinessStatusUnknown ReadinessStatus = "unknown"
	// ReadinessStatusReady means the resource is safe for its stated next stage.
	ReadinessStatusReady ReadinessStatus = "ready"
	// ReadinessStatusBlocked means required data or action is unresolved.
	ReadinessStatusBlocked ReadinessStatus = "blocked"
	// ReadinessStatusIneligible means deterministic policy excludes the tracker.
	ReadinessStatusIneligible ReadinessStatus = "ineligible"
	// ReadinessStatusStale means freshness or a dependency fingerprint no longer matches.
	ReadinessStatusStale ReadinessStatus = "stale"
)

// RequiredActionKind classifies backend-defined manual work.
type RequiredActionKind string

const (
	// RequiredActionSelectPlaylist requests a disc playlist choice.
	RequiredActionSelectPlaylist RequiredActionKind = "select_playlist"
	// RequiredActionSelectMetadata requests a metadata candidate choice.
	RequiredActionSelectMetadata RequiredActionKind = "select_metadata"
	// RequiredActionConfirmRescan requests permission for a destructive cache refresh.
	RequiredActionConfirmRescan RequiredActionKind = "confirm_rescan"
	// RequiredActionAuthenticateTracker is retained for v1 decoding compatibility.
	//
	// Deprecated: upload workflows no longer emit tracker-authentication actions.
	RequiredActionAuthenticateTracker RequiredActionKind = "authenticate_tracker"
	// RequiredActionProvideTwoFactor is retained for v1 decoding compatibility.
	//
	// Deprecated: upload workflows no longer emit tracker two-factor actions.
	RequiredActionProvideTwoFactor RequiredActionKind = "provide_two_factor"
	// RequiredActionProvideTrackerInput requests tracker-specific projection input.
	RequiredActionProvideTrackerInput RequiredActionKind = "provide_tracker_input"
	// RequiredActionAnswerQuestionnaire requests tracker questionnaire answers.
	RequiredActionAnswerQuestionnaire RequiredActionKind = "answer_questionnaire"
	// RequiredActionAuthorizeRules requests acknowledgement of waivable rules.
	RequiredActionAuthorizeRules RequiredActionKind = "authorize_rules"
	// RequiredActionReviewDuplicates requests duplicate acceptance or rejection.
	RequiredActionReviewDuplicates RequiredActionKind = "review_duplicates"
	// RequiredActionApproveTrackers requests one exact post-dupe tracker subset.
	RequiredActionApproveTrackers RequiredActionKind = "approve_trackers"
	// RequiredActionApproveUpload requests approval of the retained upload plan.
	//
	// Deprecated: upload approval is no longer emitted by runtime workflows.
	RequiredActionApproveUpload RequiredActionKind = "approve_upload"
	// RequiredActionReprepare requests a fresh generation after invalidation or restart.
	RequiredActionReprepare RequiredActionKind = "reprepare"
	// RequiredActionReconcileSubmission requests manual verification of an uncertain external submission.
	RequiredActionReconcileSubmission RequiredActionKind = "reconcile_submission"
)

// RequiredActionStatus records whether a typed action still needs an answer.
type RequiredActionStatus string

const (
	// RequiredActionStatusPending means no accepted answer exists.
	RequiredActionStatusPending RequiredActionStatus = "pending"
	// RequiredActionStatusResolved means an answer was accepted.
	RequiredActionStatusResolved RequiredActionStatus = "resolved"
	// RequiredActionStatusExpired means its expected workflow revision is stale.
	RequiredActionStatusExpired RequiredActionStatus = "expired"
)

// RequiredActionOption is one non-secret selectable action value.
type RequiredActionOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// RequiredAction is a transport-safe backend-defined manual action.
type RequiredAction struct {
	ID               RequiredActionID           `json:"id"`
	Kind             RequiredActionKind         `json:"kind"`
	Status           RequiredActionStatus       `json:"status"`
	WorkflowRevision WorkflowRevision           `json:"workflowRevision"`
	TrackerID        TrackerID                  `json:"trackerId,omitempty"`
	EffectKind       WorkflowExternalEffectKind `json:"effectKind,omitempty"`
	EffectScopeID    string                     `json:"effectScopeId,omitempty"`
	Prompt           string                     `json:"prompt"`
	Options          []RequiredActionOption     `json:"options,omitempty"`
	AllowsFreeText   bool                       `json:"allowsFreeText,omitempty"`
	CreatedAt        time.Time                  `json:"createdAt" ts_type:"string"`
	ExpiresAt        *time.Time                 `json:"expiresAt,omitempty" ts_type:"string"`
}

const (
	// RequiredActionReconcileNotCompleted confirms the external effect did not complete and may be retried.
	RequiredActionReconcileNotCompleted = "not_completed"
)

// RequiredActionAnswer submits one action answer against an exact workflow revision.
type RequiredActionAnswer struct {
	ActionID         RequiredActionID `json:"actionId"`
	WorkflowRevision WorkflowRevision `json:"workflowRevision"`
	SelectedValues   []string         `json:"selectedValues,omitempty"`
	TextValue        *string          `json:"textValue,omitempty"`
	Confirmed        *bool            `json:"confirmed,omitempty"`
}

// WorkflowFailure binds a safe structured failure to an optional tracker/resource.
type WorkflowFailure struct {
	Failure   OperationFailure `json:"failure"`
	TrackerID TrackerID        `json:"trackerId,omitempty"`
	Resource  string           `json:"resource,omitempty"`
}

// WorkflowOperationItem is one ordered safe progress row.
type WorkflowOperationItem struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"`
	Label     string      `json:"label"`
	Phase     string      `json:"phase,omitempty"`
	Status    StageStatus `json:"status"`
	Completed int         `json:"completed,omitempty"`
	Total     int         `json:"total,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// WorkflowOperationResultKind identifies the exact public result promised by
// a terminal workflow operation. Private execution authority is never exposed.
type WorkflowOperationResultKind string

const (
	WorkflowOperationResultRelease      WorkflowOperationResultKind = "release"
	WorkflowOperationResultProjections  WorkflowOperationResultKind = "tracker_projections"
	WorkflowOperationResultPreflight    WorkflowOperationResultKind = "tracker_preflight"
	WorkflowOperationResultDupes        WorkflowOperationResultKind = "dupes"
	WorkflowOperationResultMedia        WorkflowOperationResultKind = "media"
	WorkflowOperationResultDescriptions WorkflowOperationResultKind = "descriptions"
	WorkflowOperationResultDryRun       WorkflowOperationResultKind = "dry_run"
	WorkflowOperationResultUpload       WorkflowOperationResultKind = "upload_result"
)

// WorkflowOperationResult binds terminal success to one exact retained public
// snapshot. RefID is opaque and safe for adapter diagnostics.
type WorkflowOperationResult struct {
	Kind             WorkflowOperationResultKind `json:"kind"`
	WorkflowRevision WorkflowRevision            `json:"workflowRevision"`
	RefID            string                      `json:"refId"`
	RefRevision      WorkflowRevision            `json:"refRevision"`
}

// WorkflowOperationStatus is the authoritative pollable operation state.
type WorkflowOperationStatus struct {
	ID             WorkflowOperationID      `json:"id"`
	WorkflowID     WorkflowID               `json:"workflowId"`
	Revision       WorkflowRevision         `json:"revision"`
	ResultRevision WorkflowRevision         `json:"resultRevision,omitempty"`
	Sequence       uint64                   `json:"sequence"`
	Command        string                   `json:"command"`
	Operation      OperationKind            `json:"operation"`
	Phase          string                   `json:"phase,omitempty"`
	Status         StageStatus              `json:"status"`
	Progress       int                      `json:"progress"`
	Completed      int                      `json:"completed"`
	Total          int                      `json:"total"`
	Message        string                   `json:"message,omitempty"`
	Items          []WorkflowOperationItem  `json:"items,omitempty"`
	Failures       []WorkflowFailure        `json:"failures,omitempty"`
	Events         []WorkflowEvent          `json:"events,omitempty"`
	Result         *WorkflowOperationResult `json:"result,omitempty"`
	StartedAt      time.Time                `json:"startedAt" ts_type:"string"`
	UpdatedAt      time.Time                `json:"updatedAt" ts_type:"string"`
	CompletedAt    *time.Time               `json:"completedAt,omitempty" ts_type:"string"`
}

// CanonicalWorkflowFingerprint returns a deterministic SHA-256 fingerprint of value's JSON form.
func CanonicalWorkflowFingerprint(value any) (WorkflowFingerprint, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("workflow fingerprint: marshal: %w", err)
	}
	sum := sha256.Sum256(payload)
	return WorkflowFingerprint(hex.EncodeToString(sum[:])), nil
}

func validateWorkflowIdentity(workflowID WorkflowID, revision WorkflowRevision) error {
	if strings.TrimSpace(string(workflowID)) == "" {
		return errors.New("workflow id is required")
	}
	if revision == 0 {
		return errors.New("workflow revision must be positive")
	}
	return nil
}

func validateSnapshotIdentity(id string, revision WorkflowRevision, createdAt time.Time) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("snapshot id is required")
	}
	if revision == 0 {
		return errors.New("snapshot revision must be positive")
	}
	if createdAt.IsZero() {
		return errors.New("snapshot creation time is required")
	}
	return nil
}

func validateWorkflowFingerprint(value WorkflowFingerprint) error {
	decoded, err := hex.DecodeString(string(value))
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("workflow fingerprint must be a SHA-256 hex value")
	}
	return nil
}
