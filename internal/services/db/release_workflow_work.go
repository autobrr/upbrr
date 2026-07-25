// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// LoadReleaseWorkflowWork returns one exact owner-scoped work lease and checkpoint.
func (r *SQLiteRepository) LoadReleaseWorkflowWork(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowWorkRecord, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || workflowID == "" || operationID == "" {
		return api.ReleaseWorkflowWorkRecord{}, api.ErrReleaseWorkflowOperationNotFound
	}
	var record api.ReleaseWorkflowWorkRecord
	var workflowValue string
	var operationValue string
	var leaseExpiresAt string
	var updatedAt string
	var completedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT owner_id, workflow_id, operation_id, lease_owner, lease_expires_at,
			checkpoint_json, updated_at, completed_at
		FROM release_workflow_work
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
	`, ownerID, workflowID, operationID).Scan(
		&record.OwnerID,
		&workflowValue,
		&operationValue,
		&record.LeaseOwner,
		&leaseExpiresAt,
		&record.Checkpoint,
		&updatedAt,
		&completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseWorkflowWorkRecord{}, api.ErrReleaseWorkflowOperationNotFound
		}
		return api.ReleaseWorkflowWorkRecord{}, fmt.Errorf("db load release workflow work: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflowValue)
	record.OperationID = api.WorkflowOperationID(operationValue)
	record.LeaseExpiresAt, err = time.Parse(time.RFC3339Nano, leaseExpiresAt)
	if err != nil {
		return api.ReleaseWorkflowWorkRecord{}, fmt.Errorf("db parse release workflow work lease expiry: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return api.ReleaseWorkflowWorkRecord{}, fmt.Errorf("db parse release workflow work update: %w", err)
	}
	if completedAt.Valid {
		completed, parseErr := time.Parse(time.RFC3339Nano, completedAt.String)
		if parseErr != nil {
			return api.ReleaseWorkflowWorkRecord{}, fmt.Errorf("db parse release workflow work completion: %w", parseErr)
		}
		record.CompletedAt = &completed
	}
	return record, nil
}

// ClaimReleaseWorkflowWork acquires one operation lease before worker dispatch.
func (r *SQLiteRepository) ClaimReleaseWorkflowWork(
	ctx context.Context,
	record api.ReleaseWorkflowWorkRecord,
) error {
	if err := validateReleaseWorkflowWorkRecord(record, false); err != nil {
		return err
	}
	return r.withWriteTx(ctx, "claim release workflow work", func(tx *sql.Tx) error {
		var leaseOwner string
		var leaseExpiresAt string
		var completedAt sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT lease_owner, lease_expires_at, completed_at
			FROM release_workflow_work
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
		`, record.OwnerID, record.WorkflowID, record.OperationID).Scan(&leaseOwner, &leaseExpiresAt, &completedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `
				INSERT INTO release_workflow_work (
					owner_id, workflow_id, operation_id, lease_owner, lease_expires_at,
					checkpoint_json, updated_at, completed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
			`, record.OwnerID, record.WorkflowID, record.OperationID, record.LeaseOwner,
				formatWorkflowStateTime(record.LeaseExpiresAt), record.Checkpoint,
				formatWorkflowStateTime(record.UpdatedAt))
			if err != nil {
				return fmt.Errorf("db claim release workflow work insert: %w", err)
			}
			return nil
		case err != nil:
			return fmt.Errorf("db load release workflow work lease: %w", err)
		case completedAt.Valid:
			return api.ErrReleaseWorkflowOperationConflict
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, leaseExpiresAt)
		if err != nil {
			return fmt.Errorf("db parse release workflow work lease: %w", err)
		}
		if leaseOwner != record.LeaseOwner && expiresAt.After(record.UpdatedAt) {
			return api.ErrReleaseWorkflowOperationConflict
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE release_workflow_work
			SET lease_owner = ?, lease_expires_at = ?, checkpoint_json = ?, updated_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND completed_at IS NULL
		`, record.LeaseOwner, formatWorkflowStateTime(record.LeaseExpiresAt), record.Checkpoint,
			formatWorkflowStateTime(record.UpdatedAt), record.OwnerID, record.WorkflowID, record.OperationID)
		if err != nil {
			return fmt.Errorf("db claim release workflow work update: %w", err)
		}
		return requireSingleReleaseWorkflowWorkRow(result)
	})
}

// RenewReleaseWorkflowWork extends one currently owned work lease.
func (r *SQLiteRepository) RenewReleaseWorkflowWork(
	ctx context.Context,
	record api.ReleaseWorkflowWorkRecord,
) error {
	if err := validateReleaseWorkflowWorkRecord(record, false); err != nil {
		return err
	}
	result, err := r.execWrite(ctx, "renew release workflow work", `
		UPDATE release_workflow_work
		SET lease_expires_at = ?, updated_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
			AND lease_owner = ? AND completed_at IS NULL
	`, formatWorkflowStateTime(record.LeaseExpiresAt), formatWorkflowStateTime(record.UpdatedAt),
		record.OwnerID, record.WorkflowID, record.OperationID, record.LeaseOwner)
	if err != nil {
		return err
	}
	return requireSingleReleaseWorkflowWorkRow(result)
}

// CheckpointReleaseWorkflowWork persists the latest safe operation state and renews its lease.
func (r *SQLiteRepository) CheckpointReleaseWorkflowWork(
	ctx context.Context,
	record api.ReleaseWorkflowWorkRecord,
) error {
	if err := validateReleaseWorkflowWorkRecord(record, false); err != nil {
		return err
	}
	result, err := r.execWrite(ctx, "checkpoint release workflow work", `
		UPDATE release_workflow_work
		SET lease_expires_at = ?, checkpoint_json = ?, updated_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
			AND lease_owner = ? AND completed_at IS NULL
	`, formatWorkflowStateTime(record.LeaseExpiresAt), record.Checkpoint, formatWorkflowStateTime(record.UpdatedAt),
		record.OwnerID, record.WorkflowID, record.OperationID, record.LeaseOwner)
	if err != nil {
		return err
	}
	return requireSingleReleaseWorkflowWorkRow(result)
}

// CompleteReleaseWorkflowWork stores the final checkpoint and releases the work lease.
func (r *SQLiteRepository) CompleteReleaseWorkflowWork(
	ctx context.Context,
	record api.ReleaseWorkflowWorkRecord,
) error {
	if err := validateReleaseWorkflowWorkRecord(record, true); err != nil {
		return err
	}
	result, err := r.execWrite(ctx, "complete release workflow work", `
		UPDATE release_workflow_work
		SET lease_expires_at = ?, checkpoint_json = ?, updated_at = ?, completed_at = ?
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
			AND lease_owner = ? AND completed_at IS NULL
	`, formatWorkflowStateTime(record.LeaseExpiresAt), record.Checkpoint, formatWorkflowStateTime(record.UpdatedAt),
		formatWorkflowStateTime(*record.CompletedAt), record.OwnerID, record.WorkflowID, record.OperationID, record.LeaseOwner)
	if err != nil {
		return err
	}
	return requireSingleReleaseWorkflowWorkRow(result)
}

func validateReleaseWorkflowWorkRecord(record api.ReleaseWorkflowWorkRecord, terminal bool) error {
	if strings.TrimSpace(record.OwnerID) == "" || record.WorkflowID == "" || record.OperationID == "" ||
		strings.TrimSpace(record.LeaseOwner) == "" || record.UpdatedAt.IsZero() ||
		!record.LeaseExpiresAt.After(record.UpdatedAt) || len(record.Checkpoint) == 0 {
		return errors.New("db: release workflow work lease and checkpoint are incomplete")
	}
	if terminal && record.CompletedAt == nil {
		return errors.New("db: release workflow work completion timestamp is required")
	}
	return nil
}

func requireSingleReleaseWorkflowWorkRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db release workflow work rows: %w", err)
	}
	if rows != 1 {
		return api.ErrReleaseWorkflowOperationConflict
	}
	return nil
}
