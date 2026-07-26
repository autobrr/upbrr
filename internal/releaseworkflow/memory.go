// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type creationReceipt struct {
	fingerprint api.WorkflowFingerprint
	workflowID  api.WorkflowID
}

// MemoryRepository is a concurrency-safe owner-scoped workflow repository.
type MemoryRepository struct {
	mu            sync.RWMutex
	states        map[api.WorkflowID]State
	creationKeys  map[string]creationReceipt
	operations    map[api.WorkflowOperationID]api.ReleaseWorkflowOperationRecord
	intents       map[string]api.ReleaseWorkflowIntentRecord
	continuations map[api.WorkflowID]api.ReleaseWorkflowContinuationRecord
	events        map[api.WorkflowID][]api.WorkflowEvent
	eventKeys     map[api.WorkflowID]map[string]api.WorkflowEvent
	effects       map[string]api.ReleaseWorkflowEffectRecord
	work          map[string]api.ReleaseWorkflowWorkRecord
}

// NewMemoryRepository returns an empty in-memory workflow repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		states:        make(map[api.WorkflowID]State),
		creationKeys:  make(map[string]creationReceipt),
		operations:    make(map[api.WorkflowOperationID]api.ReleaseWorkflowOperationRecord),
		intents:       make(map[string]api.ReleaseWorkflowIntentRecord),
		continuations: make(map[api.WorkflowID]api.ReleaseWorkflowContinuationRecord),
		events:        make(map[api.WorkflowID][]api.WorkflowEvent),
		eventKeys:     make(map[api.WorkflowID]map[string]api.WorkflowEvent),
		effects:       make(map[string]api.ReleaseWorkflowEffectRecord),
		work:          make(map[string]api.ReleaseWorkflowWorkRecord),
	}
}

// Create atomically creates state or returns the prior result for a matching idempotency key.
func (r *MemoryRepository) Create(
	ctx context.Context,
	ownerID string,
	idempotencyKey string,
	fingerprint api.WorkflowFingerprint,
	state State,
) (State, bool, error) {
	if err := ctx.Err(); err != nil {
		return State{}, false, fmt.Errorf("create workflow: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || state.Workflow.ID == "" {
		return State{}, false, errors.New("create workflow: owner and workflow id are required")
	}
	cloned, err := cloneState(state)
	if err != nil {
		return State{}, false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	key := ownerID + "\x00" + strings.TrimSpace(idempotencyKey)
	if strings.TrimSpace(idempotencyKey) != "" {
		if prior, ok := r.creationKeys[key]; ok {
			if prior.fingerprint != fingerprint {
				return State{}, false, ErrIdempotencyConflict
			}
			priorState, ok := r.states[prior.workflowID]
			if !ok || priorState.OwnerID != ownerID {
				return State{}, false, ErrWorkflowNotFound
			}
			result, cloneErr := cloneState(priorState)
			return result, true, cloneErr
		}
	}
	if _, exists := r.states[state.Workflow.ID]; exists {
		return State{}, false, ErrRevisionConflict
	}
	cloned.OwnerID = ownerID
	r.states[state.Workflow.ID] = cloned
	if strings.TrimSpace(idempotencyKey) != "" {
		r.creationKeys[key] = creationReceipt{fingerprint: fingerprint, workflowID: state.Workflow.ID}
	}
	result, err := cloneState(cloned)
	return result, false, err
}

// Load returns one detached owner-scoped workflow state.
func (r *MemoryRepository) Load(ctx context.Context, ownerID string, workflowID api.WorkflowID) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, fmt.Errorf("load workflow: %w", err)
	}
	r.mu.RLock()
	state, ok := r.states[workflowID]
	r.mu.RUnlock()
	if !ok || state.OwnerID != strings.TrimSpace(ownerID) {
		return State{}, ErrWorkflowNotFound
	}
	return cloneState(state)
}

// Save applies one optimistic state update.
func (r *MemoryRepository) Save(ctx context.Context, ownerID string, expected api.WorkflowRevision, state State) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save workflow: %w", err)
	}
	cloned, err := cloneState(state)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.states[state.Workflow.ID]
	if !ok || current.OwnerID != strings.TrimSpace(ownerID) {
		return ErrWorkflowNotFound
	}
	if current.Workflow.Revision != expected {
		return ErrRevisionConflict
	}
	if cloned.Workflow.Revision != expected+1 {
		return errors.New("release workflow: revision must advance by one")
	}
	cloned.OwnerID = current.OwnerID
	r.states[state.Workflow.ID] = cloned
	return nil
}

// Delete removes one owner-scoped workflow and its creation receipts.
func (r *MemoryRepository) Delete(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.states[workflowID]
	if !ok || state.OwnerID != ownerID {
		return ErrWorkflowNotFound
	}
	delete(r.states, workflowID)
	for key, receipt := range r.creationKeys {
		if receipt.workflowID == workflowID {
			delete(r.creationKeys, key)
		}
	}
	for operationID, operation := range r.operations {
		if operation.WorkflowID == workflowID && operation.OwnerID == ownerID {
			delete(r.operations, operationID)
		}
	}
	delete(r.continuations, workflowID)
	delete(r.events, workflowID)
	delete(r.eventKeys, workflowID)
	for key, intent := range r.intents {
		if intent.OwnerID == ownerID && intent.WorkflowID == workflowID {
			delete(r.intents, key)
		}
	}
	for key, effect := range r.effects {
		if effect.OwnerID == ownerID && effect.WorkflowID == workflowID {
			delete(r.effects, key)
		}
	}
	for key, work := range r.work {
		if work.OwnerID == ownerID && work.WorkflowID == workflowID {
			delete(r.work, key)
		}
	}
	return nil
}

func (r *MemoryRepository) CreateOperation(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
) (api.ReleaseWorkflowOperationRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, false, fmt.Errorf("create workflow operation: %w", err)
	}
	if err := record.Status.Validate(); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, false, fmt.Errorf("create workflow operation: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prior := range r.operations {
		if prior.OwnerID != record.OwnerID || prior.WorkflowID != record.WorkflowID {
			continue
		}
		if record.IdempotencyKey != "" && prior.IdempotencyKey == record.IdempotencyKey && prior.Status.Command == record.Status.Command {
			if prior.CommandFingerprint != record.CommandFingerprint {
				return api.ReleaseWorkflowOperationRecord{}, false, ErrOperationConflict
			}
			return cloneMemoryOperationRecord(prior), true, nil
		}
		if workflowOperationActive(prior.Status.Status) {
			return api.ReleaseWorkflowOperationRecord{}, false, ErrOperationConflict
		}
	}
	if _, exists := r.operations[record.OperationID]; exists {
		return api.ReleaseWorkflowOperationRecord{}, false, ErrOperationConflict
	}
	r.operations[record.OperationID] = cloneMemoryOperationRecord(record)
	return cloneMemoryOperationRecord(record), false, nil
}

func (r *MemoryRepository) LoadOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowOperationRecord, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, fmt.Errorf("load workflow operation: %w", err)
	}
	r.mu.RLock()
	record, ok := r.operations[operationID]
	r.mu.RUnlock()
	if !ok || record.OwnerID != strings.TrimSpace(ownerID) || record.WorkflowID != workflowID {
		return api.ReleaseWorkflowOperationRecord{}, ErrWorkflowNotFound
	}
	return cloneMemoryOperationRecord(record), nil
}

// LoadOperationByIdempotency returns one exact in-memory command receipt.
func (r *MemoryRepository) LoadOperationByIdempotency(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
) (api.ReleaseWorkflowOperationRecord, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, fmt.Errorf("load workflow operation by idempotency: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	commandName = strings.TrimSpace(commandName)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, record := range r.operations {
		if record.OwnerID == ownerID && record.WorkflowID == workflowID && record.Status.Command == commandName &&
			record.IdempotencyKey == idempotencyKey {
			return cloneMemoryOperationRecord(record), nil
		}
	}
	return api.ReleaseWorkflowOperationRecord{}, ErrWorkflowNotFound
}

func (r *MemoryRepository) LoadLatestOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowOperationRecord, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, fmt.Errorf("load latest workflow operation: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest api.ReleaseWorkflowOperationRecord
	found := false
	for _, record := range r.operations {
		if record.OwnerID != ownerID || record.WorkflowID != workflowID {
			continue
		}
		if !found || record.Status.UpdatedAt.After(latest.Status.UpdatedAt) {
			latest = record
			found = true
		}
	}
	if !found {
		return api.ReleaseWorkflowOperationRecord{}, ErrWorkflowNotFound
	}
	return cloneMemoryOperationRecord(latest), nil
}

func (r *MemoryRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save workflow operation: %w", err)
	}
	if err := record.Status.Validate(); err != nil {
		return fmt.Errorf("save workflow operation: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.operations[record.OperationID]
	if !ok || current.OwnerID != record.OwnerID || current.WorkflowID != record.WorkflowID {
		return ErrWorkflowNotFound
	}
	if current.Status.Sequence != expectedSequence || record.Status.Sequence != expectedSequence+1 {
		return ErrRevisionConflict
	}
	r.operations[record.OperationID] = cloneMemoryOperationRecord(record)
	return nil
}

func (r *MemoryRepository) ListActiveOperations(ctx context.Context) ([]api.ReleaseWorkflowOperationRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list active workflow operations: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	records := make([]api.ReleaseWorkflowOperationRecord, 0)
	for _, record := range r.operations {
		if workflowOperationActive(record.Status.Status) {
			records = append(records, cloneMemoryOperationRecord(record))
		}
	}
	return records, nil
}

// AcceptIntent retains one exact desired-state request in memory.
func (r *MemoryRepository) AcceptIntent(
	ctx context.Context,
	record api.ReleaseWorkflowIntentRecord,
) (api.ReleaseWorkflowIntentRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowIntentRecord{}, false, fmt.Errorf("accept workflow intent: %w", err)
	}
	key := memoryIntentKey(record.OwnerID, record.WorkflowID, record.IdempotencyKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	if prior, ok := r.intents[key]; ok {
		if prior.RequestFingerprint != record.RequestFingerprint {
			return api.ReleaseWorkflowIntentRecord{}, false, ErrIdempotencyConflict
		}
		prior.IntentPayload = append([]byte(nil), prior.IntentPayload...)
		return prior, true, nil
	}
	record.IntentPayload = append([]byte(nil), record.IntentPayload...)
	r.intents[key] = record
	return record, false, nil
}

// SaveContinuation materializes one in-memory continuation projection.
func (r *MemoryRepository) SaveContinuation(ctx context.Context, record api.ReleaseWorkflowContinuationRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save workflow continuation: %w", err)
	}
	record.Payload = append([]byte(nil), record.Payload...)
	r.mu.Lock()
	defer r.mu.Unlock()
	prior, ok := r.continuations[record.WorkflowID]
	if ok && prior.OwnerID == record.OwnerID && prior.Revision > record.Revision {
		return nil
	}
	r.continuations[record.WorkflowID] = record
	return nil
}

// AppendEvents assigns workflow-global event cursors and retains immutable copies.
func (r *MemoryRepository) AppendEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	events []api.WorkflowEvent,
) ([]api.WorkflowEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("append workflow events: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.states[workflowID]
	if !ok || state.OwnerID != ownerID {
		return nil, ErrWorkflowNotFound
	}
	retained := r.events[workflowID]
	keys := r.eventKeys[workflowID]
	if keys == nil {
		keys = make(map[string]api.WorkflowEvent)
	}
	appended := make([]api.WorkflowEvent, 0, len(events))
	for _, event := range events {
		eventKey := fmt.Sprintf("%s:%d", event.OperationID, event.Sequence)
		if prior, ok := keys[eventKey]; ok {
			appended = append(appended, prior)
			continue
		}
		event.Sequence = uint64(len(retained) + 1)
		retained = append(retained, event)
		keys[eventKey] = event
		appended = append(appended, event)
	}
	r.events[workflowID] = retained
	r.eventKeys[workflowID] = keys
	return appended, nil
}

// LoadEvents returns retained events after one workflow cursor.
func (r *MemoryRepository) LoadEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("load workflow events: %w", err)
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.states[workflowID]
	if !ok || state.OwnerID != strings.TrimSpace(ownerID) {
		return nil, ErrWorkflowNotFound
	}
	result := make([]api.WorkflowEvent, 0, min(limit, len(r.events[workflowID])))
	for _, event := range r.events[workflowID] {
		if event.Sequence <= after {
			continue
		}
		result = append(result, event)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

// BeginEffect fences one in-memory external attempt.
func (r *MemoryRepository) BeginEffect(
	ctx context.Context,
	record api.ReleaseWorkflowEffectRecord,
) (api.ReleaseWorkflowEffectRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowEffectRecord{}, false, fmt.Errorf("begin workflow effect: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest api.ReleaseWorkflowEffectRecord
	found := false
	for _, prior := range r.effects {
		if prior.OwnerID != record.OwnerID || prior.WorkflowID != record.WorkflowID || prior.Kind != record.Kind ||
			prior.ScopeID != record.ScopeID {
			continue
		}
		if !found || prior.StartedAt.After(latest.StartedAt) {
			latest = prior
			found = true
		}
	}
	if found {
		switch latest.Status {
		case api.WorkflowEffectStatusStarted, api.WorkflowEffectStatusUnknown:
			return api.ReleaseWorkflowEffectRecord{}, false, api.ErrReleaseWorkflowEffectOutcomeUnknown
		case api.WorkflowEffectStatusSucceeded:
			if latest.SemanticFingerprint != record.SemanticFingerprint {
				break
			}
			return latest, true, nil
		case api.WorkflowEffectStatusFailed:
		}
	}
	record.Status = api.WorkflowEffectStatusStarted
	r.effects[memoryEffectKey(record.OwnerID, record.WorkflowID, record.EffectID)] = record
	return record, false, nil
}

// CompleteEffect retains one known terminal in-memory receipt.
func (r *MemoryRepository) CompleteEffect(
	ctx context.Context,
	status api.WorkflowEffectStatus,
	record api.ReleaseWorkflowEffectRecord,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("complete workflow effect: %w", err)
	}
	key := memoryEffectKey(record.OwnerID, record.WorkflowID, record.EffectID)
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.effects[key]
	if !ok || current.Status != api.WorkflowEffectStatusStarted {
		return api.ErrReleaseWorkflowEffectConflict
	}
	current.Status = status
	current.UpdatedAt = record.UpdatedAt
	current.CompletedAt = record.CompletedAt
	r.effects[key] = current
	return nil
}

// MarkOperationEffectsUnknown fences in-memory attempts interrupted by restart.
func (r *MemoryRepository) MarkOperationEffectsUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	now time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mark workflow effects unknown: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, effect := range r.effects {
		if effect.OwnerID == ownerID && effect.WorkflowID == workflowID && effect.OperationID == operationID &&
			effect.Status == api.WorkflowEffectStatusStarted {
			effect.Status = api.WorkflowEffectStatusUnknown
			effect.UpdatedAt = now
			completed := now
			effect.CompletedAt = &completed
			r.effects[key] = effect
		}
	}
	return nil
}

// ResolveEffectUnknown records manual verification for one uncertain in-memory effect.
func (r *MemoryRepository) ResolveEffectUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	kind api.WorkflowExternalEffectKind,
	scopeID string,
	now time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolve workflow effect unknown: %w", err)
	}
	ownerID = strings.TrimSpace(ownerID)
	scopeID = strings.TrimSpace(scopeID)
	if ownerID == "" || workflowID == "" || kind == "" || scopeID == "" || now.IsZero() {
		return api.ErrReleaseWorkflowEffectConflict
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var latestKey string
	var latest api.ReleaseWorkflowEffectRecord
	for key, effect := range r.effects {
		if effect.OwnerID != ownerID || effect.WorkflowID != workflowID || effect.Kind != string(kind) ||
			effect.ScopeID != scopeID {
			continue
		}
		if latestKey == "" || effect.StartedAt.After(latest.StartedAt) {
			latestKey = key
			latest = effect
		}
	}
	if latestKey == "" {
		return api.ErrReleaseWorkflowEffectConflict
	}
	switch latest.Status {
	case api.WorkflowEffectStatusFailed:
		return nil
	case api.WorkflowEffectStatusUnknown:
		completed := now
		latest.Status = api.WorkflowEffectStatusFailed
		latest.UpdatedAt = now
		latest.CompletedAt = &completed
		r.effects[latestKey] = latest
		return nil
	case api.WorkflowEffectStatusStarted, api.WorkflowEffectStatusSucceeded:
		return api.ErrReleaseWorkflowEffectConflict
	}
	return api.ErrReleaseWorkflowEffectConflict
}

// LoadWork returns one detached in-memory operation lease and checkpoint.
func (r *MemoryRepository) LoadWork(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowWorkRecord, error) {
	if err := ctx.Err(); err != nil {
		return api.ReleaseWorkflowWorkRecord{}, fmt.Errorf("load workflow work: %w", err)
	}
	key := memoryWorkKey(ownerID, workflowID, operationID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.work[key]
	if !ok {
		return api.ReleaseWorkflowWorkRecord{}, ErrWorkflowNotFound
	}
	record.Checkpoint = append([]byte(nil), record.Checkpoint...)
	if record.CompletedAt != nil {
		completed := *record.CompletedAt
		record.CompletedAt = &completed
	}
	return record, nil
}

// ClaimWork acquires one in-memory operation lease.
func (r *MemoryRepository) ClaimWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("claim workflow work: %w", err)
	}
	key := memoryWorkKey(record.OwnerID, record.WorkflowID, record.OperationID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if prior, ok := r.work[key]; ok {
		if prior.CompletedAt != nil ||
			(prior.LeaseOwner != record.LeaseOwner && prior.LeaseExpiresAt.After(record.UpdatedAt)) {
			return ErrOperationConflict
		}
	}
	record.Checkpoint = append([]byte(nil), record.Checkpoint...)
	r.work[key] = record
	return nil
}

// RenewWork extends one currently owned in-memory operation lease.
func (r *MemoryRepository) RenewWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("renew workflow work: %w", err)
	}
	key := memoryWorkKey(record.OwnerID, record.WorkflowID, record.OperationID)
	r.mu.Lock()
	defer r.mu.Unlock()
	prior, ok := r.work[key]
	if !ok || prior.LeaseOwner != record.LeaseOwner || prior.CompletedAt != nil {
		return ErrOperationConflict
	}
	prior.LeaseExpiresAt = record.LeaseExpiresAt
	prior.UpdatedAt = record.UpdatedAt
	r.work[key] = prior
	return nil
}

// CheckpointWork retains the latest safe in-memory operation checkpoint.
func (r *MemoryRepository) CheckpointWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("checkpoint workflow work: %w", err)
	}
	key := memoryWorkKey(record.OwnerID, record.WorkflowID, record.OperationID)
	r.mu.Lock()
	defer r.mu.Unlock()
	prior, ok := r.work[key]
	if !ok || prior.LeaseOwner != record.LeaseOwner || prior.CompletedAt != nil {
		return ErrOperationConflict
	}
	prior.LeaseExpiresAt = record.LeaseExpiresAt
	prior.Checkpoint = append([]byte(nil), record.Checkpoint...)
	prior.UpdatedAt = record.UpdatedAt
	r.work[key] = prior
	return nil
}

// CompleteWork retains the terminal checkpoint and closes its in-memory lease.
func (r *MemoryRepository) CompleteWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("complete workflow work: %w", err)
	}
	key := memoryWorkKey(record.OwnerID, record.WorkflowID, record.OperationID)
	r.mu.Lock()
	defer r.mu.Unlock()
	prior, ok := r.work[key]
	if !ok || prior.LeaseOwner != record.LeaseOwner || prior.CompletedAt != nil || record.CompletedAt == nil {
		return ErrOperationConflict
	}
	prior.LeaseExpiresAt = record.LeaseExpiresAt
	prior.Checkpoint = append([]byte(nil), record.Checkpoint...)
	prior.UpdatedAt = record.UpdatedAt
	completed := *record.CompletedAt
	prior.CompletedAt = &completed
	r.work[key] = prior
	return nil
}

func memoryIntentKey(ownerID string, workflowID api.WorkflowID, idempotencyKey string) string {
	return strings.TrimSpace(ownerID) + "\x00" + string(workflowID) + "\x00" + strings.TrimSpace(idempotencyKey)
}

func memoryEffectKey(ownerID string, workflowID api.WorkflowID, effectID string) string {
	return strings.TrimSpace(ownerID) + "\x00" + string(workflowID) + "\x00" + strings.TrimSpace(effectID)
}

func memoryWorkKey(ownerID string, workflowID api.WorkflowID, operationID api.WorkflowOperationID) string {
	return strings.TrimSpace(ownerID) + "\x00" + string(workflowID) + "\x00" + string(operationID)
}

func workflowOperationActive(status api.StageStatus) bool {
	return status == api.StageStatusQueued || status == api.StageStatusRunning
}

func cloneMemoryOperationRecord(record api.ReleaseWorkflowOperationRecord) api.ReleaseWorkflowOperationRecord {
	status, err := record.Status.Clone()
	if err == nil {
		record.Status = status
	}
	return record
}

func cloneState(state State) (State, error) {
	var cloned State
	payload, err := json.Marshal(state)
	if err != nil {
		return State{}, fmt.Errorf("clone workflow state: marshal: %w", err)
	}
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return State{}, fmt.Errorf("clone workflow state: unmarshal: %w", err)
	}
	return cloned, nil
}

type privateResourceKey struct {
	ownerID    string
	workflowID api.WorkflowID
	resourceID string
}

type privateResourceEntry struct {
	value     any
	expiresAt time.Time
}

// MemoryPrivateResourceStore retains process-local, single-use workflow authority.
type MemoryPrivateResourceStore struct {
	mu       sync.Mutex
	entries  map[privateResourceKey]privateResourceEntry
	consumed map[privateResourceKey]struct{}
}

// NewMemoryPrivateResourceStore returns an empty process-local private store.
func NewMemoryPrivateResourceStore() *MemoryPrivateResourceStore {
	return &MemoryPrivateResourceStore{
		entries:  make(map[privateResourceKey]privateResourceEntry),
		consumed: make(map[privateResourceKey]struct{}),
	}
}

// Put retains one owner-scoped private resource.
func (s *MemoryPrivateResourceStore) Put(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	value any,
	expiresAt time.Time,
) error {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return err
	}
	if expiresAt.IsZero() {
		return errors.New("private resource expiry is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = privateResourceEntry{value: value, expiresAt: expiresAt}
	delete(s.consumed, key)
	return nil
}

// Get returns an unconsumed, unexpired owner-scoped resource.
func (s *MemoryPrivateResourceStore) Get(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	now time.Time,
) (any, error) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(key, now)
}

// Consume atomically returns and invalidates one single-use resource.
func (s *MemoryPrivateResourceStore) Consume(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	now time.Time,
) (any, error) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.getLocked(key, now)
	if err != nil {
		return nil, err
	}
	delete(s.entries, key)
	s.consumed[key] = struct{}{}
	return value, nil
}

// Delete removes one resource without marking its single-use key consumed.
func (s *MemoryPrivateResourceStore) Delete(ownerID string, workflowID api.WorkflowID, resourceID string) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return
	}
	s.mu.Lock()
	entry, ok := s.entries[key]
	delete(s.entries, key)
	delete(s.consumed, key)
	s.mu.Unlock()
	if ok {
		releasePrivateResource(entry.value)
	}
}

func (s *MemoryPrivateResourceStore) getLocked(key privateResourceKey, now time.Time) (any, error) {
	if _, ok := s.consumed[key]; ok {
		return nil, ErrPrivateResourceConsumed
	}
	entry, ok := s.entries[key]
	if !ok {
		return nil, ErrPrivateResourceUnavailable
	}
	if !entry.expiresAt.After(now) {
		delete(s.entries, key)
		releasePrivateResource(entry.value)
		return nil, ErrPrivateResourceUnavailable
	}
	return entry.value, nil
}

// InvalidateWorkflow removes every private resource owned by one workflow.
func (s *MemoryPrivateResourceStore) InvalidateWorkflow(ownerID string, workflowID api.WorkflowID) {
	s.InvalidateWorkflowExcept(ownerID, workflowID)
}

// InvalidateWorkflowExcept removes workflow resources except explicitly
// preserved IDs, including their existing expiry or consumed state.
func (s *MemoryPrivateResourceStore) InvalidateWorkflowExcept(
	ownerID string,
	workflowID api.WorkflowID,
	preservedResourceIDs ...string,
) {
	ownerID = strings.TrimSpace(ownerID)
	preserved := make(map[privateResourceKey]struct{}, len(preservedResourceIDs))
	for _, resourceID := range preservedResourceIDs {
		key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
		if err == nil {
			preserved[key] = struct{}{}
		}
	}
	s.mu.Lock()
	resources := make([]any, 0)
	for key, entry := range s.entries {
		if key.ownerID == ownerID && key.workflowID == workflowID {
			if _, ok := preserved[key]; ok {
				continue
			}
			resources = append(resources, entry.value)
			delete(s.entries, key)
		}
	}
	for key := range s.consumed {
		if key.ownerID == ownerID && key.workflowID == workflowID {
			if _, ok := preserved[key]; ok {
				continue
			}
			delete(s.consumed, key)
		}
	}
	s.mu.Unlock()
	for _, resource := range resources {
		releasePrivateResource(resource)
	}
}

// InvalidateAll models process restart by removing all private execution authority.
func (s *MemoryPrivateResourceStore) InvalidateAll() {
	s.mu.Lock()
	resources := make([]any, 0, len(s.entries))
	for _, entry := range s.entries {
		resources = append(resources, entry.value)
	}
	clear(s.entries)
	clear(s.consumed)
	s.mu.Unlock()
	for _, resource := range resources {
		releasePrivateResource(resource)
	}
}

func releasePrivateResource(value any) {
	if resource, ok := value.(interface{ Release() error }); ok {
		_ = resource.Release()
	}
}

func newPrivateResourceKey(ownerID string, workflowID api.WorkflowID, resourceID string) (privateResourceKey, error) {
	key := privateResourceKey{
		ownerID:    strings.TrimSpace(ownerID),
		workflowID: workflowID,
		resourceID: strings.TrimSpace(resourceID),
	}
	if key.ownerID == "" || key.workflowID == "" || key.resourceID == "" {
		return privateResourceKey{}, errors.New("private resource owner, workflow, and id are required")
	}
	return key, nil
}
