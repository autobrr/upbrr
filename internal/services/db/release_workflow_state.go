// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type workflowStateQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CreateReleaseWorkflowState atomically creates one workflow or returns the
// existing state for a matching owner-scoped creation key.
func (r *SQLiteRepository) CreateReleaseWorkflowState(
	ctx context.Context,
	record api.ReleaseWorkflowStateRecord,
) (api.ReleaseWorkflowStateRecord, bool, error) {
	if err := validateWorkflowStateRecord(record); err != nil {
		return api.ReleaseWorkflowStateRecord{}, false, err
	}
	var result api.ReleaseWorkflowStateRecord
	var idempotent bool
	err := r.withWriteTx(ctx, "create release workflow state", func(tx *sql.Tx) error {
		if record.CreationKey != "" {
			prior, err := loadWorkflowStateByCreationKey(ctx, tx, record.OwnerID, record.CreationKey)
			switch {
			case err == nil:
				if prior.CreationFingerprint != record.CreationFingerprint {
					return api.ErrReleaseWorkflowIdempotencyConflict
				}
				result = prior
				idempotent = true
				return nil
			case !errors.Is(err, api.ErrReleaseWorkflowStateNotFound):
				return err
			}
		}
		if _, err := loadWorkflowState(ctx, tx, record.OwnerID, record.WorkflowID); err == nil {
			return api.ErrReleaseWorkflowRevisionConflict
		} else if !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
			return err
		}

		_, err := tx.ExecContext(ctx, `
			INSERT INTO release_workflow_states (
				owner_id, workflow_id, revision, status, creation_key,
				creation_fingerprint, state_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.OwnerID, record.WorkflowID, record.Revision, record.Status, record.CreationKey,
			record.CreationFingerprint, record.Payload, formatWorkflowStateTime(record.CreatedAt), formatWorkflowStateTime(record.UpdatedAt))
		if err != nil {
			return fmt.Errorf("db create release workflow state: %w", err)
		}
		result = cloneWorkflowStateRecord(record)
		return nil
	})
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, false, err
	}
	return result, idempotent, nil
}

// LoadReleaseWorkflowState returns one exact owner-scoped workflow row.
func (r *SQLiteRepository) LoadReleaseWorkflowState(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowStateRecord, error) {
	return loadWorkflowState(ctx, r.db, strings.TrimSpace(ownerID), workflowID)
}

// SaveReleaseWorkflowState applies one optimistic revision update.
func (r *SQLiteRepository) SaveReleaseWorkflowState(
	ctx context.Context,
	expected api.WorkflowRevision,
	record api.ReleaseWorkflowStateRecord,
) error {
	if err := validateWorkflowStateRecord(record); err != nil {
		return err
	}
	if record.Revision != expected+1 {
		return errors.New("db: release workflow revision must advance by one")
	}
	return r.withWriteTx(ctx, "save release workflow state", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE release_workflow_states
			SET revision = ?, status = ?, state_json = ?, updated_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND revision = ?
		`, record.Revision, record.Status, record.Payload, formatWorkflowStateTime(record.UpdatedAt),
			record.OwnerID, record.WorkflowID, expected)
		if err != nil {
			return fmt.Errorf("db save release workflow state: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db save release workflow state rows: %w", err)
		}
		if rows == 1 {
			return nil
		}
		if _, err := loadWorkflowState(ctx, tx, record.OwnerID, record.WorkflowID); err != nil {
			return err
		}
		return api.ErrReleaseWorkflowRevisionConflict
	})
}

// DeleteReleaseWorkflowState removes one owner-scoped workflow.
func (r *SQLiteRepository) DeleteReleaseWorkflowState(ctx context.Context, ownerID string, workflowID api.WorkflowID) error {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || strings.TrimSpace(string(workflowID)) == "" {
		return errors.New("db: release workflow owner and id are required")
	}
	result, err := r.execWrite(ctx, "delete release workflow state", `
		DELETE FROM release_workflow_states WHERE owner_id = ? AND workflow_id = ?
	`, ownerID, workflowID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db delete release workflow state rows: %w", err)
	}
	if rows == 0 {
		return api.ErrReleaseWorkflowStateNotFound
	}
	return nil
}

// DeleteTerminalReleaseWorkflowStatesBefore deletes bounded terminal audit
// rows. Draft, active, and blocked workflows are never retention candidates.
func (r *SQLiteRepository) DeleteTerminalReleaseWorkflowStatesBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("db: release workflow retention cutoff is required")
	}
	result, err := r.execWrite(ctx, "delete terminal release workflow states", `
		DELETE FROM release_workflow_states
		WHERE status IN (?, ?, ?) AND updated_at < ?
	`, api.WorkflowStatusCompleted, api.WorkflowStatusCanceled, api.WorkflowStatusFailed, formatWorkflowStateTime(before))
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("db delete terminal release workflow states rows: %w", err)
	}
	return rows, nil
}

func loadWorkflowState(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowStateRecord, error) {
	if ownerID == "" || strings.TrimSpace(string(workflowID)) == "" {
		return api.ReleaseWorkflowStateRecord{}, api.ErrReleaseWorkflowStateNotFound
	}
	return scanWorkflowState(queryer.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, revision, status, creation_key,
			creation_fingerprint, state_json, created_at, updated_at
		FROM release_workflow_states
		WHERE owner_id = ? AND workflow_id = ?
	`, ownerID, workflowID))
}

func loadWorkflowStateByCreationKey(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	creationKey string,
) (api.ReleaseWorkflowStateRecord, error) {
	return scanWorkflowState(queryer.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, revision, status, creation_key,
			creation_fingerprint, state_json, created_at, updated_at
		FROM release_workflow_states
		WHERE owner_id = ? AND creation_key = ?
	`, ownerID, creationKey))
}

func scanWorkflowState(row *sql.Row) (api.ReleaseWorkflowStateRecord, error) {
	var record api.ReleaseWorkflowStateRecord
	var workflowID string
	var revision uint64
	var status string
	var fingerprint string
	var createdAt string
	var updatedAt string
	if err := row.Scan(&record.OwnerID, &workflowID, &revision, &status, &record.CreationKey,
		&fingerprint, &record.Payload, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseWorkflowStateRecord{}, api.ErrReleaseWorkflowStateNotFound
		}
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db load release workflow state: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflowID)
	record.Revision = api.WorkflowRevision(revision)
	record.Status = api.WorkflowStatus(status)
	record.CreationFingerprint = api.WorkflowFingerprint(fingerprint)
	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db load release workflow state created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db load release workflow state updated_at: %w", err)
	}
	return cloneWorkflowStateRecord(record), nil
}

func validateWorkflowStateRecord(record api.ReleaseWorkflowStateRecord) error {
	if strings.TrimSpace(record.OwnerID) == "" || strings.TrimSpace(string(record.WorkflowID)) == "" {
		return errors.New("db: release workflow owner and id are required")
	}
	if record.Revision == 0 || uint64(record.Revision) > math.MaxInt64 {
		return errors.New("db: release workflow revision is invalid")
	}
	if record.Status == "" || len(record.Payload) == 0 || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("db: release workflow status, payload, and timestamps are required")
	}
	if record.CreationKey != "" && record.CreationFingerprint == "" {
		return errors.New("db: release workflow creation fingerprint is required with creation key")
	}
	return nil
}

func cloneWorkflowStateRecord(record api.ReleaseWorkflowStateRecord) api.ReleaseWorkflowStateRecord {
	record.Payload = append([]byte(nil), record.Payload...)
	return record
}

func formatWorkflowStateTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
