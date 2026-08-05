// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// PersistentRepository stores safe public workflow state in the shared
// repository capability. Private resources remain process-local.
type PersistentRepository struct {
	states     api.ReleaseWorkflowStateRepository
	operations api.ReleaseWorkflowOperationRepository
	durability api.ReleaseWorkflowDurabilityRepository
}

// NewPersistentRepository constructs a durable public workflow repository.
func NewPersistentRepository(states api.ReleaseWorkflowStateRepository) (*PersistentRepository, error) {
	if states == nil {
		return nil, errors.New("release workflow: durable state repository is required")
	}
	operations, ok := states.(api.ReleaseWorkflowOperationRepository)
	if !ok || operations == nil {
		return nil, errors.New("release workflow: durable operation repository is required")
	}
	durability, ok := states.(api.ReleaseWorkflowDurabilityRepository)
	if !ok || durability == nil {
		return nil, errors.New("release workflow: durable intent, event, and effect repository is required")
	}
	return &PersistentRepository{
		states:     states,
		operations: operations,
		durability: durability,
	}, nil
}

// Create atomically creates state or returns the prior matching creation.
func (r *PersistentRepository) Create(
	ctx context.Context,
	ownerID string,
	idempotencyKey string,
	fingerprint api.WorkflowFingerprint,
	state State,
) (State, bool, error) {
	record, err := workflowStateRecord(ownerID, state)
	if err != nil {
		return State{}, false, err
	}
	record.CreationKey = strings.TrimSpace(idempotencyKey)
	record.CreationFingerprint = fingerprint
	created, idempotent, err := r.states.CreateReleaseWorkflowState(ctx, record)
	if err != nil {
		return State{}, false, mapPersistentRepositoryError(err)
	}
	result, err := decodeWorkflowState(created)
	return result, idempotent, err
}

// Load returns one detached owner-scoped durable state.
func (r *PersistentRepository) Load(ctx context.Context, ownerID string, workflowID api.WorkflowID) (State, error) {
	record, err := r.states.LoadReleaseWorkflowState(ctx, strings.TrimSpace(ownerID), workflowID)
	if err != nil {
		return State{}, mapPersistentRepositoryError(err)
	}
	return decodeWorkflowState(record)
}

// Save applies one optimistic durable state update.
func (r *PersistentRepository) Save(ctx context.Context, ownerID string, expected api.WorkflowRevision, state State) error {
	record, err := workflowStateRecord(ownerID, state)
	if err != nil {
		return err
	}
	if err := r.states.SaveReleaseWorkflowState(ctx, expected, record); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

// Delete removes one owner-scoped durable workflow.
func (r *PersistentRepository) Delete(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	if err := r.states.DeleteReleaseWorkflowState(ctx, strings.TrimSpace(ownerID), workflowID); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

func (r *PersistentRepository) CreateOperation(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
) (api.ReleaseWorkflowOperationRecord, bool, error) {
	created, idempotent, err := r.operations.CreateReleaseWorkflowOperation(ctx, record)
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, false, mapPersistentRepositoryError(err)
	}
	return created, idempotent, nil
}

func (r *PersistentRepository) LoadOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowOperationRecord, error) {
	record, err := r.operations.LoadReleaseWorkflowOperation(ctx, strings.TrimSpace(ownerID), workflowID, operationID)
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, mapPersistentRepositoryError(err)
	}
	return record, nil
}

// LoadOperationByIdempotency returns one exact durable command receipt.
func (r *PersistentRepository) LoadOperationByIdempotency(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
) (api.ReleaseWorkflowOperationRecord, error) {
	record, err := r.operations.LoadReleaseWorkflowOperationByIdempotency(
		ctx,
		ownerID,
		workflowID,
		commandName,
		idempotencyKey,
	)
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, mapPersistentRepositoryError(err)
	}
	return record, nil
}

func (r *PersistentRepository) LoadLatestOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowOperationRecord, error) {
	record, err := r.operations.LoadLatestReleaseWorkflowOperation(ctx, strings.TrimSpace(ownerID), workflowID)
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, mapPersistentRepositoryError(err)
	}
	return record, nil
}

func (r *PersistentRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	if err := r.operations.SaveReleaseWorkflowOperation(ctx, expectedSequence, record); err != nil {
		return mapPersistentRepositoryError(err)
	}
	return nil
}

func (r *PersistentRepository) ListActiveOperations(ctx context.Context) ([]api.ReleaseWorkflowOperationRecord, error) {
	records, err := r.operations.ListActiveReleaseWorkflowOperations(ctx)
	if err != nil {
		return nil, mapPersistentRepositoryError(err)
	}
	return records, nil
}

// AcceptIntent persists one exact desired-state request.
func (r *PersistentRepository) AcceptIntent(
	ctx context.Context,
	record api.ReleaseWorkflowIntentRecord,
) (api.ReleaseWorkflowIntentRecord, bool, error) {
	if r.durability == nil {
		return api.ReleaseWorkflowIntentRecord{}, false, errors.New("release workflow: durable intent repository is unavailable")
	}
	accepted, idempotent, err := r.durability.AcceptReleaseWorkflowIntent(ctx, record)
	return accepted, idempotent, mapPersistentRepositoryError(err)
}

// SaveContinuation materializes one safe continuation projection.
func (r *PersistentRepository) SaveContinuation(ctx context.Context, record api.ReleaseWorkflowContinuationRecord) error {
	if r.durability == nil {
		return errors.New("release workflow: durable continuation repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.SaveReleaseWorkflowContinuation(ctx, record))
}

// AppendEvents retains immutable workflow-scoped events.
func (r *PersistentRepository) AppendEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	events []api.WorkflowEvent,
) ([]api.WorkflowEvent, error) {
	if r.durability == nil {
		return nil, errors.New("release workflow: durable event repository is unavailable")
	}
	appended, err := r.durability.AppendReleaseWorkflowEvents(ctx, ownerID, workflowID, events)
	return appended, mapPersistentRepositoryError(err)
}

// LoadEvents returns retained immutable events after one cursor.
func (r *PersistentRepository) LoadEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	if r.durability == nil {
		return nil, errors.New("release workflow: durable event repository is unavailable")
	}
	events, err := r.durability.LoadReleaseWorkflowEvents(ctx, ownerID, workflowID, after, limit)
	return events, mapPersistentRepositoryError(err)
}

// BeginEffect durably fences one exact external attempt.
func (r *PersistentRepository) BeginEffect(
	ctx context.Context,
	record api.ReleaseWorkflowEffectRecord,
) (api.ReleaseWorkflowEffectRecord, bool, error) {
	if r.durability == nil {
		return api.ReleaseWorkflowEffectRecord{}, false, errors.New("release workflow: durable effect repository is unavailable")
	}
	started, idempotent, err := r.durability.BeginReleaseWorkflowEffect(ctx, record)
	return started, idempotent, mapPersistentRepositoryError(err)
}

// CompleteEffect persists one known terminal external-attempt receipt.
func (r *PersistentRepository) CompleteEffect(
	ctx context.Context,
	status api.WorkflowEffectStatus,
	record api.ReleaseWorkflowEffectRecord,
) error {
	if r.durability == nil {
		return errors.New("release workflow: durable effect repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.CompleteReleaseWorkflowEffect(ctx, status, record))
}

// MarkOperationEffectsUnknown fences started effects interrupted by restart.
func (r *PersistentRepository) MarkOperationEffectsUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	now time.Time,
) error {
	if r.durability == nil {
		return errors.New("release workflow: durable effect repository is unavailable")
	}
	return mapPersistentRepositoryError(
		r.durability.MarkReleaseWorkflowOperationEffectsUnknown(ctx, ownerID, workflowID, operationID, now),
	)
}

// ResolveEffectUnknown records manual verification that an uncertain effect did not complete.
func (r *PersistentRepository) ResolveEffectUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	kind api.WorkflowExternalEffectKind,
	scopeID string,
	now time.Time,
) error {
	if r.durability == nil {
		return errors.New("release workflow: durable effect repository is unavailable")
	}
	return mapPersistentRepositoryError(
		r.durability.ResolveReleaseWorkflowEffectUnknown(ctx, ownerID, workflowID, kind, scopeID, now),
	)
}

func (r *PersistentRepository) LoadWork(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowWorkRecord, error) {
	if r.durability == nil {
		return api.ReleaseWorkflowWorkRecord{}, errors.New("release workflow: durable work repository is unavailable")
	}
	record, err := r.durability.LoadReleaseWorkflowWork(ctx, ownerID, workflowID, operationID)
	if err != nil {
		return api.ReleaseWorkflowWorkRecord{}, mapPersistentRepositoryError(err)
	}
	return record, nil
}

func (r *PersistentRepository) ClaimWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.durability == nil {
		return errors.New("release workflow: durable work repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.ClaimReleaseWorkflowWork(ctx, record))
}

func (r *PersistentRepository) RenewWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.durability == nil {
		return errors.New("release workflow: durable work repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.RenewReleaseWorkflowWork(ctx, record))
}

func (r *PersistentRepository) CheckpointWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.durability == nil {
		return errors.New("release workflow: durable work repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.CheckpointReleaseWorkflowWork(ctx, record))
}

func (r *PersistentRepository) CompleteWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.durability == nil {
		return errors.New("release workflow: durable work repository is unavailable")
	}
	return mapPersistentRepositoryError(r.durability.CompleteReleaseWorkflowWork(ctx, record))
}

func workflowStateRecord(ownerID string, state State) (api.ReleaseWorkflowStateRecord, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("persist workflow state: marshal: %w", err)
	}
	return api.ReleaseWorkflowStateRecord{
		OwnerID:    strings.TrimSpace(ownerID),
		WorkflowID: state.Workflow.ID,
		Revision:   state.Workflow.Revision,
		Status:     state.Workflow.Status,
		Payload:    payload,
		CreatedAt:  state.Workflow.CreatedAt,
		UpdatedAt:  state.Workflow.UpdatedAt,
	}, nil
}

func decodeWorkflowState(record api.ReleaseWorkflowStateRecord) (State, error) {
	var state State
	if err := json.Unmarshal(record.Payload, &state); err != nil {
		return State{}, fmt.Errorf("persist workflow state: unmarshal: %w", err)
	}
	if state.OwnerID != record.OwnerID || state.Workflow.ID != record.WorkflowID || state.Workflow.Revision != record.Revision {
		return State{}, errors.New("persist workflow state: row metadata does not match payload")
	}
	normalizeState(&state)
	return state, nil
}

func mapPersistentRepositoryError(err error) error {
	switch {
	case errors.Is(err, api.ErrReleaseWorkflowStateNotFound):
		return ErrWorkflowNotFound
	case errors.Is(err, api.ErrReleaseWorkflowRevisionConflict):
		return ErrRevisionConflict
	case errors.Is(err, api.ErrReleaseWorkflowIdempotencyConflict):
		return ErrIdempotencyConflict
	case errors.Is(err, api.ErrReleaseWorkflowOperationNotFound):
		return ErrWorkflowNotFound
	case errors.Is(err, api.ErrReleaseWorkflowOperationConflict):
		return ErrOperationConflict
	case errors.Is(err, api.ErrReleaseWorkflowOperationSequenceConflict):
		return ErrRevisionConflict
	case errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown):
		return api.ErrReleaseWorkflowEffectOutcomeUnknown
	case errors.Is(err, api.ErrReleaseWorkflowEffectAlreadySucceeded):
		return api.ErrReleaseWorkflowEffectAlreadySucceeded
	case errors.Is(err, api.ErrReleaseWorkflowEffectConflict):
		return api.ErrReleaseWorkflowEffectConflict
	default:
		return err
	}
}

func normalizeState(state *State) {
	if state.TrackerDecisionMode == "" && state.Composite != nil {
		state.TrackerDecisionMode = TrackerDecisionModePostDupeGate
	} else {
		state.TrackerDecisionMode = normalizeTrackerDecisionMode(state.TrackerDecisionMode)
	}
	state.Workflow.RequiredActions = slices.DeleteFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveUpload //nolint:staticcheck // Migrate retained v1 final-approval actions.
	})
	if state.Workflow.Status == api.WorkflowStatusBlocked &&
		!hasPendingRequiredAction(state.Workflow.RequiredActions) &&
		len(state.Workflow.Failures) == 0 {
		state.Workflow.Status = api.WorkflowStatusActive
	}
	if state.FactInstructions == nil {
		state.FactInstructions = make(map[api.ReleaseFactInstructionSnapshotID]api.ReleaseFactInstructionSnapshot)
	}
	if state.Releases == nil {
		state.Releases = make(map[api.ReleaseSnapshotID]api.ReleaseSnapshot)
	}
	if state.Catalogs == nil {
		state.Catalogs = make(map[api.TrackerCatalogSnapshotID]api.TrackerCatalogSnapshot)
	}
	if state.Runtimes == nil {
		state.Runtimes = make(map[api.TrackerRuntimeSnapshotID]api.TrackerRuntimeSnapshot)
	}
	if state.Selections == nil {
		state.Selections = make(map[api.TrackerSelectionID]api.TrackerSelection)
	}
	if state.ProjectionInstructions == nil {
		state.ProjectionInstructions = make(map[api.TrackerProjectionInstructionSnapshotID]api.TrackerProjectionInstructionSnapshot)
	}
	if state.Projections == nil {
		state.Projections = make(map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet)
	}
	if state.Preflights == nil {
		state.Preflights = make(map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment)
	}
	if state.Dupes == nil {
		state.Dupes = make(map[api.DupeAssessmentID]api.DupeAssessment)
	}
	if state.TrackerApprovals == nil {
		state.TrackerApprovals = make(map[api.TrackerApprovalSnapshotID]api.TrackerApprovalSnapshot)
	}
	if state.Media == nil {
		state.Media = make(map[api.MediaArtifactSetID]api.MediaArtifactSet)
	}
	if state.Descriptions == nil {
		state.Descriptions = make(map[api.DescriptionSetID]api.DescriptionSet)
	}
	if state.DryRuns == nil {
		state.DryRuns = make(map[api.UploadDryRunResultID]api.UploadDryRunResult)
	}
	if state.UploadResults == nil {
		state.UploadResults = make(map[api.UploadResultID]api.UploadResult)
	}
	if state.Operations == nil {
		state.Operations = make(map[api.WorkflowOperationID]api.WorkflowOperationStatus)
	}
	if state.Receipts == nil {
		state.Receipts = make(map[string]commandReceipt)
	}
}
