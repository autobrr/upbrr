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
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, false); err != nil {
			return err
		}
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
	_, err := r.execWorkflowInputWrite(ctx, "save release workflow continuation", record.OwnerID, record.WorkflowID, `
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
		if err := requireWorkflowInputMutation(ctx, tx, ownerID, workflowID, true); err != nil {
			return err
		}
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
	if record.Submission != nil {
		return r.beginSubmissionFence(ctx, record)
	}
	var result api.ReleaseWorkflowEffectRecord
	var idempotent bool
	err := r.withWriteTx(ctx, "begin release workflow effect", func(tx *sql.Tx) error {
		if err := requireConfigActivationGeneration(ctx, tx); err != nil {
			return err
		}
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, false); err != nil {
			return err
		}
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

// beginSubmissionFence atomically claims the global content/site fence and
// stores the workflow-local receipt before a tracker request is made.
func (r *SQLiteRepository) beginSubmissionFence(
	ctx context.Context,
	record api.ReleaseWorkflowEffectRecord,
) (api.ReleaseWorkflowEffectRecord, bool, error) {
	authority := *record.Submission
	var result api.ReleaseWorkflowEffectRecord
	var idempotent bool
	err := r.withWriteTx(ctx, "begin submission fence", func(tx *sql.Tx) error {
		if err := requireConfigActivationGeneration(ctx, tx); err != nil {
			return err
		}
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, false); err != nil {
			return err
		}
		if err := requireActiveInputFence(ctx, tx, record.OwnerID, record.WorkflowID,
			authority.CoordinatorID, authority.Fence, record.UpdatedAt); err != nil {
			return err
		}
		prior, err := loadSubmissionFence(ctx, tx, authority.ContentIdentity, authority.TrackerSite)
		switch {
		case err == nil && prior.Status == api.WorkflowEffectStatusSucceeded:
			result = record
			result.EffectID = prior.EffectID
			result.Status = prior.Status
			idempotent = true
			return nil
		case err == nil && (prior.Status == api.WorkflowEffectStatusStarted || prior.Status == api.WorkflowEffectStatusUnknown):
			return api.ErrReleaseWorkflowEffectOutcomeUnknown
		case err == nil:
			return api.ErrReleaseWorkflowEffectConflict
		case !errors.Is(err, api.ErrSubmissionFenceNotFound):
			return err
		}
		payload, marshalErr := json.Marshal(authority.ContentIdentity)
		if marshalErr != nil {
			return fmt.Errorf("db encode submission content identity: %w", marshalErr)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO submission_fences (
				content_version, content_digest, content_scope, content_json, tracker_site,
				owner_id, workflow_id, operation_id, effect_id, status, started_at, updated_at, confirmed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		`, authority.ContentIdentity.Version, authority.ContentIdentity.Digest, authority.ContentIdentity.Scope, payload,
			authority.TrackerSite, record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID,
			api.WorkflowEffectStatusStarted, formatWorkflowStateTime(record.StartedAt), formatWorkflowStateTime(record.UpdatedAt)); err != nil {
			return fmt.Errorf("db begin submission fence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO release_workflow_effects (
				owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
				semantic_fingerprint, status, started_at, updated_at, completed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		`, record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, record.Kind, record.ScopeID,
			record.SemanticFingerprint, api.WorkflowEffectStatusStarted,
			formatWorkflowStateTime(record.StartedAt), formatWorkflowStateTime(record.UpdatedAt)); err != nil {
			return fmt.Errorf("db begin release workflow submission effect: %w", err)
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
	if record.Submission != nil {
		return r.completeSubmissionFence(ctx, status, record)
	}
	return r.withWriteTx(ctx, "complete release workflow effect", func(tx *sql.Tx) error {
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, true); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE release_workflow_effects
			SET status = ?, updated_at = ?, completed_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
		`, status, formatWorkflowStateTime(record.UpdatedAt), formatWorkflowStateTime(*record.CompletedAt),
			record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, api.WorkflowEffectStatusStarted)
		if err != nil {
			return fmt.Errorf("db complete release workflow effect: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db complete release workflow effect rows: %w", err)
		}
		if rows != 1 {
			return api.ErrReleaseWorkflowEffectConflict
		}
		return nil
	})
}

// completeSubmissionFence atomically records confirmed remote success with its
// workflow receipt. A known failed attempt removes only its exact global row.
func (r *SQLiteRepository) completeSubmissionFence(
	ctx context.Context,
	status api.WorkflowEffectStatus,
	record api.ReleaseWorkflowEffectRecord,
) error {
	authority := *record.Submission
	return r.withWriteTx(ctx, "complete submission fence", func(tx *sql.Tx) error {
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, true); err != nil {
			return err
		}
		if err := requireActiveInputFence(ctx, tx, record.OwnerID, record.WorkflowID,
			authority.CoordinatorID, authority.Fence, record.UpdatedAt); err != nil {
			return err
		}
		var result sql.Result
		var err error
		if status == api.WorkflowEffectStatusSucceeded {
			result, err = tx.ExecContext(ctx, `
				UPDATE submission_fences
				SET status = ?, updated_at = ?, confirmed_at = ?
				WHERE content_version = ? AND content_digest = ? AND tracker_site = ?
					AND owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
			`, status, formatWorkflowStateTime(record.UpdatedAt), formatWorkflowStateTime(*record.CompletedAt),
				authority.ContentIdentity.Version, authority.ContentIdentity.Digest, authority.TrackerSite,
				record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, api.WorkflowEffectStatusStarted)
		} else {
			result, err = tx.ExecContext(ctx, `
				DELETE FROM submission_fences
				WHERE content_version = ? AND content_digest = ? AND tracker_site = ?
					AND owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
			`, authority.ContentIdentity.Version, authority.ContentIdentity.Digest, authority.TrackerSite,
				record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, api.WorkflowEffectStatusStarted)
		}
		if err != nil {
			return fmt.Errorf("db complete submission fence: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db complete submission fence rows: %w", err)
		}
		if rows != 1 {
			return api.ErrReleaseWorkflowEffectConflict
		}
		result, err = tx.ExecContext(ctx, `
			UPDATE release_workflow_effects
			SET status = ?, updated_at = ?, completed_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
		`, status, formatWorkflowStateTime(record.UpdatedAt), formatWorkflowStateTime(*record.CompletedAt),
			record.OwnerID, record.WorkflowID, record.OperationID, record.EffectID, api.WorkflowEffectStatusStarted)
		if err != nil {
			return fmt.Errorf("db complete release workflow submission effect: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db complete release workflow submission effect rows: %w", err)
		}
		if rows != 1 {
			return api.ErrReleaseWorkflowEffectConflict
		}
		return nil
	})
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
	return r.withWriteTx(ctx, "mark release workflow effects unknown", func(tx *sql.Tx) error {
		if err := requireWorkflowInputMutation(ctx, tx, ownerID, workflowID, true); err != nil {
			return err
		}
		return markReleaseWorkflowOperationEffectsUnknown(ctx, tx, ownerID, workflowID, operationID, now)
	})
}

func markReleaseWorkflowOperationEffectsUnknown(
	ctx context.Context,
	tx *sql.Tx,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	now time.Time,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE submission_fences SET status = ?, updated_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND status = ?
	`, api.WorkflowEffectStatusUnknown, formatWorkflowStateTime(now), strings.TrimSpace(ownerID), workflowID,
		operationID, api.WorkflowEffectStatusStarted); err != nil {
		return fmt.Errorf("db mark submission fences unknown: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE release_workflow_effects
		SET status = ?, updated_at = ?, completed_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND status = ?
	`, api.WorkflowEffectStatusUnknown, formatWorkflowStateTime(now), formatWorkflowStateTime(now),
		strings.TrimSpace(ownerID), workflowID, operationID, api.WorkflowEffectStatusStarted); err != nil {
		return fmt.Errorf("db mark release workflow effects unknown: %w", err)
	}
	return nil
}

// RecoverLegacyReleaseWorkflowEffects marks every pre-active-input started
// effect unknown and returns the exact owner-scoped effects to reconcile. It
// retains the existing effect identities and never creates submission fences.
func (r *SQLiteRepository) RecoverLegacyReleaseWorkflowEffects(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	now time.Time,
) ([]api.ReleaseWorkflowEffectRecord, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" || now.IsZero() {
		return nil, errors.New("db: legacy effect recovery owner, workflow, and timestamp are required")
	}
	var effects []api.ReleaseWorkflowEffectRecord
	err := r.withWriteTx(ctx, "recover legacy release workflow effects", func(tx *sql.Tx) error {
		if err := requireLegacyInputRecovery(ctx, tx, ownerID, workflowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE submission_fences
			SET status = ?, updated_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND status = ?
		`, api.WorkflowEffectStatusUnknown, formatWorkflowStateTime(now), ownerID, workflowID, api.WorkflowEffectStatusStarted); err != nil {
			return fmt.Errorf("db recover legacy submission fences: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE release_workflow_effects
			SET status = ?, updated_at = ?, completed_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND status = ?
		`, api.WorkflowEffectStatusUnknown, formatWorkflowStateTime(now), formatWorkflowStateTime(now),
			ownerID, workflowID, api.WorkflowEffectStatusStarted); err != nil {
			return fmt.Errorf("db recover legacy release workflow effects: %w", err)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
				semantic_fingerprint, status, started_at, updated_at, completed_at
			FROM release_workflow_effects
			WHERE owner_id = ? AND workflow_id = ? AND status = ?
			ORDER BY started_at, effect_id
		`, ownerID, workflowID, api.WorkflowEffectStatusUnknown)
		if err != nil {
			return fmt.Errorf("db list legacy release workflow effects: %w", err)
		}
		defer rows.Close()
		effects = make([]api.ReleaseWorkflowEffectRecord, 0)
		for rows.Next() {
			effect, scanErr := scanReleaseWorkflowEffect(rows)
			if scanErr != nil {
				return scanErr
			}
			effects = append(effects, effect)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("db iterate legacy release workflow effects: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return effects, nil
}

// ListLegacyReleaseWorkflowRecoveryWorkflowIDs returns only the caller's
// workflows with effects that need a migrated-slot reconciliation.
func (r *SQLiteRepository) ListLegacyReleaseWorkflowRecoveryWorkflowIDs(ctx context.Context, ownerID string) ([]api.WorkflowID, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return nil, api.ErrReleaseWorkflowStateNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT workflow_id
		FROM release_workflow_effects
		WHERE owner_id = ? AND status IN ('started', 'unknown')
		ORDER BY workflow_id
	`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("db list legacy recovery workflows: %w", err)
	}
	defer rows.Close()
	workflowIDs := make([]api.WorkflowID, 0)
	for rows.Next() {
		var workflowID string
		if err := rows.Scan(&workflowID); err != nil {
			return nil, fmt.Errorf("db scan legacy recovery workflow: %w", err)
		}
		workflowIDs = append(workflowIDs, api.WorkflowID(workflowID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate legacy recovery workflows: %w", err)
	}
	return workflowIDs, nil
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
		if err := requireWorkflowInputMutation(ctx, tx, ownerID, workflowID, true); err != nil {
			return err
		}
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
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM submission_fences
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ? AND status = ?
		`, ownerID, workflowID, latest.OperationID, latest.EffectID, api.WorkflowEffectStatusUnknown); err != nil {
			return fmt.Errorf("db resolve unknown submission fence: %w", err)
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

type workflowEffectScanner interface {
	Scan(...any) error
}

func loadLatestReleaseWorkflowEffect(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	kind string,
	scopeID string,
) (api.ReleaseWorkflowEffectRecord, error) {
	return scanReleaseWorkflowEffect(queryer.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, operation_id, effect_id, kind, scope_id,
			semantic_fingerprint, status, started_at, updated_at, completed_at
		FROM release_workflow_effects
		WHERE owner_id = ? AND workflow_id = ? AND kind = ? AND scope_id = ?
		ORDER BY started_at DESC, effect_id DESC
		LIMIT 1
	`, ownerID, workflowID, kind, scopeID))
}

func scanReleaseWorkflowEffect(scanner workflowEffectScanner) (api.ReleaseWorkflowEffectRecord, error) {
	var record api.ReleaseWorkflowEffectRecord
	var workflow string
	var operation string
	var semanticFingerprint string
	var status string
	var startedAt string
	var updatedAt string
	var completedAt sql.NullString
	err := scanner.Scan(
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

// LoadSubmissionFence returns the minimal global exclusion state for one exact
// canonical submitted-content and tracker-site identity.
func (r *SQLiteRepository) LoadSubmissionFence(
	ctx context.Context,
	identity api.SubmissionContentIdentity,
	trackerSite string,
) (api.SubmissionFenceRecord, error) {
	if err := validateSubmissionFenceLookup(identity, trackerSite); err != nil {
		return api.SubmissionFenceRecord{}, err
	}
	return loadSubmissionFence(ctx, r.db, identity, trackerSite)
}

func loadSubmissionFence(
	ctx context.Context,
	queryer workflowStateQueryer,
	identity api.SubmissionContentIdentity,
	trackerSite string,
) (api.SubmissionFenceRecord, error) {
	var record api.SubmissionFenceRecord
	var workflow string
	var operation string
	var status string
	var content []byte
	var startedAt string
	var updatedAt string
	var confirmedAt sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT content_json, tracker_site, owner_id, workflow_id, operation_id, effect_id,
			status, started_at, updated_at, confirmed_at
		FROM submission_fences
		WHERE content_version = ? AND content_digest = ? AND tracker_site = ?
	`, identity.Version, identity.Digest, strings.TrimSpace(trackerSite)).Scan(
		&content, &record.TrackerSite, &record.OwnerID, &workflow, &operation, &record.EffectID,
		&status, &startedAt, &updatedAt, &confirmedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return api.SubmissionFenceRecord{}, api.ErrSubmissionFenceNotFound
	}
	if err != nil {
		return api.SubmissionFenceRecord{}, fmt.Errorf("db load submission fence: %w", err)
	}
	if err := json.Unmarshal(content, &record.ContentIdentity); err != nil {
		return api.SubmissionFenceRecord{}, fmt.Errorf("db decode submission fence identity: %w", err)
	}
	if err := validateSubmissionFenceLookup(record.ContentIdentity, record.TrackerSite); err != nil {
		return api.SubmissionFenceRecord{}, fmt.Errorf("db invalid submission fence: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflow)
	record.OperationID = api.WorkflowOperationID(operation)
	record.Status = api.WorkflowEffectStatus(status)
	var parseErr error
	record.StartedAt, parseErr = time.Parse(time.RFC3339Nano, startedAt)
	if parseErr != nil {
		return api.SubmissionFenceRecord{}, fmt.Errorf("db load submission fence started_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedAt)
	if parseErr != nil {
		return api.SubmissionFenceRecord{}, fmt.Errorf("db load submission fence updated_at: %w", parseErr)
	}
	if confirmedAt.Valid {
		confirmed, parseErr := time.Parse(time.RFC3339Nano, confirmedAt.String)
		if parseErr != nil {
			return api.SubmissionFenceRecord{}, fmt.Errorf("db load submission fence confirmed_at: %w", parseErr)
		}
		record.ConfirmedAt = &confirmed
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
	if record.Submission != nil {
		if record.Kind != string(api.WorkflowExternalEffectTrackerSubmission) {
			return errors.New("db: submission fence requires tracker submission effect")
		}
		if err := record.Submission.Validate(); err != nil {
			return fmt.Errorf("db: submission fence authority: %w", err)
		}
	}
	return nil
}

func validateSubmissionFenceLookup(identity api.SubmissionContentIdentity, trackerSite string) error {
	if strings.TrimSpace(trackerSite) == "" {
		return errors.New("db: submission fence tracker site is required")
	}
	canonical, err := api.NewSubmissionContentIdentity(identity.Scope, identity.Files)
	if err != nil {
		return fmt.Errorf("db: submission fence identity: %w", err)
	}
	if identity.Version != canonical.Version || identity.Digest != canonical.Digest {
		return errors.New("db: submission fence identity is not canonical")
	}
	return nil
}

func cloneReleaseWorkflowIntentRecord(record api.ReleaseWorkflowIntentRecord) api.ReleaseWorkflowIntentRecord {
	record.IntentPayload = append([]byte(nil), record.IntentPayload...)
	return record
}
