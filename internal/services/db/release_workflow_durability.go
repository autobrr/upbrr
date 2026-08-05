// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// AcceptReleaseWorkflowIntent atomically retains one exact Continue request.
func (r *SQLiteRepository) AcceptReleaseWorkflowIntent(
	ctx context.Context,
	record api.ReleaseWorkflowIntentRecord,
) (api.ReleaseWorkflowIntentRecord, bool, error) {
	if err := validateReleaseWorkflowIntentRecord(record); err != nil {
		return api.ReleaseWorkflowIntentRecord{}, false, err
	}
	var result api.ReleaseWorkflowIntentRecord
	var idempotent bool
	err := r.withWriteTx(ctx, "accept release workflow intent", func(tx *sql.Tx) error {
		prior, loadErr := loadReleaseWorkflowIntent(ctx, tx, record.OwnerID, record.WorkflowID, record.IdempotencyKey)
		switch {
		case loadErr == nil:
			if prior.RequestFingerprint != record.RequestFingerprint {
				return api.ErrReleaseWorkflowIdempotencyConflict
			}
			result = prior
			idempotent = true
			return nil
		case !errors.Is(loadErr, api.ErrReleaseWorkflowStateNotFound):
			return loadErr
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO release_workflow_intents (
				owner_id, workflow_id, idempotency_key, request_fingerprint,
				goal, intent_json, accepted_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, record.OwnerID, record.WorkflowID, record.IdempotencyKey, record.RequestFingerprint,
			record.Goal, record.IntentPayload, formatWorkflowStateTime(record.AcceptedAt))
		if insertErr != nil {
			return fmt.Errorf("db accept release workflow intent: %w", insertErr)
		}
		result = cloneReleaseWorkflowIntentRecord(record)
		return nil
	})
	if err != nil {
		return api.ReleaseWorkflowIntentRecord{}, false, err
	}
	return result, idempotent, nil
}

// SaveReleaseWorkflowContinuation materializes the latest safe continuation projection.
func (r *SQLiteRepository) SaveReleaseWorkflowContinuation(
	ctx context.Context,
	record api.ReleaseWorkflowContinuationRecord,
) error {
	if err := validateReleaseWorkflowContinuationRecord(record); err != nil {
		return err
	}
	_, err := r.execWrite(ctx, "save release workflow continuation", `
		INSERT INTO release_workflow_continuations (
			owner_id, workflow_id, revision, continuation_json, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (owner_id, workflow_id) DO UPDATE SET
			revision = excluded.revision,
			continuation_json = excluded.continuation_json,
			updated_at = excluded.updated_at
		WHERE excluded.revision >= release_workflow_continuations.revision
	`, record.OwnerID, record.WorkflowID, record.Revision, record.Payload, formatWorkflowStateTime(record.UpdatedAt))
	return err
}

// AppendReleaseWorkflowEvents appends immutable events and assigns workflow-global sequences.
func (r *SQLiteRepository) AppendReleaseWorkflowEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	events []api.WorkflowEvent,
) ([]api.WorkflowEvent, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || strings.TrimSpace(string(workflowID)) == "" {
		return nil, errors.New("db: release workflow event owner and workflow are required")
	}
	if len(events) == 0 {
		return nil, nil
	}
	appended := make([]api.WorkflowEvent, 0, len(events))
	err := r.withWriteTx(ctx, "append release workflow events", func(tx *sql.Tx) error {
		var next uint64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sequence), 0)
			FROM release_workflow_events
			WHERE owner_id = ? AND workflow_id = ?
		`, ownerID, workflowID).Scan(&next); err != nil {
			return fmt.Errorf("db load release workflow event cursor: %w", err)
		}
		for _, event := range events {
			if event.WorkflowID != workflowID || event.OperationID == "" || event.Sequence == 0 || event.Timestamp.IsZero() {
				return errors.New("db: release workflow event identity, source sequence, and timestamp are required")
			}
			eventKey := fmt.Sprintf("%s:%d", event.OperationID, event.Sequence)
			prior, loadErr := loadReleaseWorkflowEventByKey(ctx, tx, ownerID, workflowID, eventKey)
			switch {
			case loadErr == nil:
				appended = append(appended, prior)
				continue
			case !errors.Is(loadErr, api.ErrReleaseWorkflowStateNotFound):
				return loadErr
			}
			if next == math.MaxInt64 {
				return errors.New("db: release workflow event cursor exhausted")
			}
			next++
			event.Sequence = next
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return fmt.Errorf("db encode release workflow event: %w", marshalErr)
			}
			_, insertErr := tx.ExecContext(ctx, `
				INSERT INTO release_workflow_events (
					owner_id, workflow_id, sequence, event_key, operation_id, event_json, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, ownerID, workflowID, event.Sequence, eventKey, event.OperationID, payload,
				formatWorkflowStateTime(event.Timestamp))
			if insertErr != nil {
				return fmt.Errorf("db append release workflow event: %w", insertErr)
			}
			appended = append(appended, event)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return appended, nil
}

// LoadReleaseWorkflowEvents returns immutable events after one workflow cursor.
func (r *SQLiteRepository) LoadReleaseWorkflowEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" {
		return nil, errors.New("db: release workflow event owner and workflow are required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_json
		FROM release_workflow_events
		WHERE owner_id = ? AND workflow_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, ownerID, workflowID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("db load release workflow events: %w", err)
	}
	defer rows.Close()
	events := make([]api.WorkflowEvent, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("db scan release workflow event: %w", err)
		}
		var event api.WorkflowEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("db decode release workflow event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate release workflow events: %w", err)
	}
	return events, nil
}

// BeginReleaseWorkflowEffect persists attempt_started before an external side effect.
func (r *SQLiteRepository) BeginReleaseWorkflowEffect(
	ctx context.Context,
	record api.ReleaseWorkflowEffectRecord,
) (api.ReleaseWorkflowEffectRecord, bool, error) {
	if err := validateReleaseWorkflowEffectRecord(record, false); err != nil {
		return api.ReleaseWorkflowEffectRecord{}, false, err
	}
	var result api.ReleaseWorkflowEffectRecord
	var idempotent bool
	err := r.withWriteTx(ctx, "begin release workflow effect", func(tx *sql.Tx) error {
		prior, loadErr := loadLatestReleaseWorkflowEffect(
			ctx,
			tx,
			record.OwnerID,
			record.WorkflowID,
			record.Kind,
			record.ScopeID,
		)
		switch {
		case loadErr == nil && (prior.Status == api.WorkflowEffectStatusStarted || prior.Status == api.WorkflowEffectStatusUnknown):
			return api.ErrReleaseWorkflowEffectOutcomeUnknown
		case loadErr == nil && prior.Status == api.WorkflowEffectStatusSucceeded &&
			prior.SemanticFingerprint == record.SemanticFingerprint:
			result = prior
			idempotent = true
			return nil
		case loadErr == nil && (prior.Status == api.WorkflowEffectStatusFailed ||
			prior.Status == api.WorkflowEffectStatusSucceeded):
		case errors.Is(loadErr, api.ErrReleaseWorkflowStateNotFound):
		default:
			return loadErr
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO release_workflow_effects (
				owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
				semantic_fingerprint, status, started_at, updated_at, completed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		`, record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, record.Kind, record.ScopeID,
			record.SemanticFingerprint, api.WorkflowEffectStatusStarted,
			formatWorkflowStateTime(record.StartedAt), formatWorkflowStateTime(record.UpdatedAt))
		if insertErr != nil {
			return fmt.Errorf("db begin release workflow effect: %w", insertErr)
		}
		record.Status = api.WorkflowEffectStatusStarted
		result = record
		return nil
	})
	if err != nil {
		return api.ReleaseWorkflowEffectRecord{}, false, err
	}
	return result, idempotent, nil
}

// CompleteReleaseWorkflowEffect persists a known terminal receipt.
func (r *SQLiteRepository) CompleteReleaseWorkflowEffect(
	ctx context.Context,
	status api.WorkflowEffectStatus,
	record api.ReleaseWorkflowEffectRecord,
) error {
	if status != api.WorkflowEffectStatusSucceeded && status != api.WorkflowEffectStatusFailed {
		return errors.New("db: release workflow effect terminal status is invalid")
	}
	record.Status = status
	if err := validateReleaseWorkflowEffectRecord(record, true); err != nil {
		return err
	}
	result, err := r.execWrite(ctx, "complete release workflow effect", `
		UPDATE release_workflow_effects
		SET status = ?, updated_at = ?, completed_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
	`, status, formatWorkflowStateTime(record.UpdatedAt), formatWorkflowStateTime(*record.CompletedAt),
		record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, api.WorkflowEffectStatusStarted)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db complete release workflow effect rows: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return api.ErrReleaseWorkflowEffectConflict
}

// MarkReleaseWorkflowOperationEffectsUnknown fences attempts interrupted by restart.
func (r *SQLiteRepository) MarkReleaseWorkflowOperationEffectsUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	now time.Time,
) error {
	if strings.TrimSpace(ownerID) == "" || workflowID == "" || operationID == "" || now.IsZero() {
		return errors.New("db: release workflow unknown effect identity and timestamp are required")
	}
	_, err := r.execWrite(ctx, "mark release workflow effects unknown", `
		UPDATE release_workflow_effects
		SET status = ?, updated_at = ?, completed_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND status = ?
	`, api.WorkflowEffectStatusUnknown, formatWorkflowStateTime(now), formatWorkflowStateTime(now),
		strings.TrimSpace(ownerID), workflowID, operationID, api.WorkflowEffectStatusStarted)
	return err
}

// ResolveReleaseWorkflowEffectUnknown records manual verification that the
// latest uncertain effect did not complete, allowing a fresh exact attempt.
func (r *SQLiteRepository) ResolveReleaseWorkflowEffectUnknown(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	kind api.WorkflowExternalEffectKind,
	scopeID string,
	now time.Time,
) error {
	ownerID = strings.TrimSpace(ownerID)
	scopeID = strings.TrimSpace(scopeID)
	if ownerID == "" || workflowID == "" || kind == "" || scopeID == "" || now.IsZero() {
		return errors.New("db: release workflow effect reconciliation identity and timestamp are required")
	}
	return r.withWriteTx(ctx, "resolve unknown release workflow effect", func(tx *sql.Tx) error {
		latest, err := loadLatestReleaseWorkflowEffect(ctx, tx, ownerID, workflowID, string(kind), scopeID)
		if err != nil {
			return err
		}
		switch latest.Status {
		case api.WorkflowEffectStatusFailed:
			return nil
		case api.WorkflowEffectStatusUnknown:
		case api.WorkflowEffectStatusStarted, api.WorkflowEffectStatusSucceeded:
			return api.ErrReleaseWorkflowEffectConflict
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE release_workflow_effects
			SET status = ?, updated_at = ?, completed_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
		`, api.WorkflowEffectStatusFailed, formatWorkflowStateTime(now), formatWorkflowStateTime(now),
			ownerID, workflowID, latest.OperationID, latest.EffectID, api.WorkflowEffectStatusUnknown)
		if err != nil {
			return fmt.Errorf("db resolve unknown release workflow effect: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db resolve unknown release workflow effect rows: %w", err)
		}
		if rows != 1 {
			return api.ErrReleaseWorkflowEffectConflict
		}
		return nil
	})
}

func loadReleaseWorkflowIntent(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	idempotencyKey string,
) (api.ReleaseWorkflowIntentRecord, error) {
	var record api.ReleaseWorkflowIntentRecord
	var workflow string
	var fingerprint string
	var goal string
	var acceptedAt string
	err := queryer.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, idempotency_key, request_fingerprint, goal, intent_json, accepted_at
		FROM release_workflow_intents
		WHERE owner_id = ? AND workflow_id = ? AND idempotency_key = ?
	`, ownerID, workflowID, idempotencyKey).Scan(
		&record.OwnerID,
		&workflow,
		&record.IdempotencyKey,
		&fingerprint,
		&goal,
		&record.IntentPayload,
		&acceptedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseWorkflowIntentRecord{}, api.ErrReleaseWorkflowStateNotFound
		}
		return api.ReleaseWorkflowIntentRecord{}, fmt.Errorf("db load release workflow intent: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflow)
	record.RequestFingerprint = api.WorkflowFingerprint(fingerprint)
	record.Goal = api.WorkflowGoal(goal)
	record.AcceptedAt, err = time.Parse(time.RFC3339Nano, acceptedAt)
	if err != nil {
		return api.ReleaseWorkflowIntentRecord{}, fmt.Errorf("db load release workflow intent timestamp: %w", err)
	}
	return cloneReleaseWorkflowIntentRecord(record), nil
}

func loadReleaseWorkflowEventByKey(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	eventKey string,
) (api.WorkflowEvent, error) {
	var payload []byte
	err := queryer.QueryRowContext(ctx, `
		SELECT event_json
		FROM release_workflow_events
		WHERE owner_id = ? AND workflow_id = ? AND event_key = ?
	`, ownerID, workflowID, eventKey).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.WorkflowEvent{}, api.ErrReleaseWorkflowStateNotFound
		}
		return api.WorkflowEvent{}, fmt.Errorf("db load release workflow event: %w", err)
	}
	var event api.WorkflowEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return api.WorkflowEvent{}, fmt.Errorf("db decode release workflow event: %w", err)
	}
	return event, nil
}

func loadLatestReleaseWorkflowEffect(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	kind string,
	scopeID string,
) (api.ReleaseWorkflowEffectRecord, error) {
	var record api.ReleaseWorkflowEffectRecord
	var workflow string
	var operation string
	var semanticFingerprint string
	var status string
	var startedAt string
	var updatedAt string
	var completedAt sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
			semantic_fingerprint, status, started_at, updated_at, completed_at
		FROM release_workflow_effects
		WHERE owner_id = ? AND workflow_id = ? AND kind = ? AND scope_id = ?
		ORDER BY started_at DESC, effect_id DESC
		LIMIT 1
	`, ownerID, workflowID, kind, scopeID).Scan(
		&record.OwnerID,
		&workflow,
		&operation,
		&record.EffectID,
		&record.Kind,
		&record.ScopeID,
		&semanticFingerprint,
		&status,
		&startedAt,
		&updatedAt,
		&completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseWorkflowEffectRecord{}, api.ErrReleaseWorkflowStateNotFound
		}
		return api.ReleaseWorkflowEffectRecord{}, fmt.Errorf("db load release workflow effect: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflow)
	record.OperationID = api.WorkflowOperationID(operation)
	record.SemanticFingerprint = api.WorkflowFingerprint(semanticFingerprint)
	record.Status = api.WorkflowEffectStatus(status)
	record.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return api.ReleaseWorkflowEffectRecord{}, fmt.Errorf("db load release workflow effect started_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return api.ReleaseWorkflowEffectRecord{}, fmt.Errorf("db load release workflow effect updated_at: %w", err)
	}
	if completedAt.Valid {
		completed, parseErr := time.Parse(time.RFC3339Nano, completedAt.String)
		if parseErr != nil {
			return api.ReleaseWorkflowEffectRecord{}, fmt.Errorf("db load release workflow effect completed_at: %w", parseErr)
		}
		record.CompletedAt = &completed
	}
	return record, nil
}

func validateReleaseWorkflowIntentRecord(record api.ReleaseWorkflowIntentRecord) error {
	if strings.TrimSpace(record.OwnerID) == "" || record.WorkflowID == "" || strings.TrimSpace(record.IdempotencyKey) == "" ||
		record.RequestFingerprint == "" || record.Goal == "" || len(record.IntentPayload) == 0 || record.AcceptedAt.IsZero() {
		return errors.New("db: release workflow accepted intent is incomplete")
	}
	return nil
}

func validateReleaseWorkflowContinuationRecord(record api.ReleaseWorkflowContinuationRecord) error {
	if strings.TrimSpace(record.OwnerID) == "" || record.WorkflowID == "" || record.Revision == 0 ||
		uint64(record.Revision) > math.MaxInt64 || len(record.Payload) == 0 || record.UpdatedAt.IsZero() {
		return errors.New("db: release workflow continuation is incomplete")
	}
	return nil
}

func validateReleaseWorkflowEffectRecord(record api.ReleaseWorkflowEffectRecord, terminal bool) error {
	if strings.TrimSpace(record.OwnerID) == "" || record.WorkflowID == "" || record.OperationID == "" ||
		strings.TrimSpace(record.EffectID) == "" || strings.TrimSpace(record.Kind) == "" ||
		strings.TrimSpace(record.ScopeID) == "" || record.SemanticFingerprint == "" ||
		record.StartedAt.IsZero() || record.UpdatedAt.Before(record.StartedAt) {
		return errors.New("db: release workflow effect is incomplete")
	}
	if terminal && record.CompletedAt == nil {
		return errors.New("db: release workflow effect terminal timestamp is required")
	}
	return nil
}

func cloneReleaseWorkflowIntentRecord(record api.ReleaseWorkflowIntentRecord) api.ReleaseWorkflowIntentRecord {
	record.IntentPayload = append([]byte(nil), record.IntentPayload...)
	return record
}
