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

type workflowOperationScanner interface {
	Scan(...any) error
}

// CreateReleaseWorkflowOperation persists queued state before work begins. An
// exact idempotent repeat returns the existing operation.
func (r *SQLiteRepository) CreateReleaseWorkflowOperation(
	ctx context.Context,
	record api.ReleaseWorkflowOperationRecord,
) (api.ReleaseWorkflowOperationRecord, bool, error) {
	payload, err := encodeWorkflowOperationRecord(record)
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, false, err
	}
	var result api.ReleaseWorkflowOperationRecord
	var idempotent bool
	err = r.withWriteTx(ctx, "create release workflow operation", func(tx *sql.Tx) error {
		if record.IdempotencyKey != "" {
			prior, loadErr := loadWorkflowOperationByIdempotency(
				ctx,
				tx,
				record.OwnerID,
				record.WorkflowID,
				record.Status.Command,
				record.IdempotencyKey,
			)
			switch {
			case loadErr == nil:
				if prior.CommandFingerprint != record.CommandFingerprint {
					return api.ErrReleaseWorkflowOperationConflict
				}
				result = prior
				idempotent = true
				return nil
			case !errors.Is(loadErr, api.ErrReleaseWorkflowOperationNotFound):
				return loadErr
			}
		}
		if _, loadErr := loadActiveWorkflowOperation(ctx, tx, record.OwnerID, record.WorkflowID); loadErr == nil {
			return api.ErrReleaseWorkflowOperationConflict
		} else if !errors.Is(loadErr, api.ErrReleaseWorkflowOperationNotFound) {
			return loadErr
		}
		if _, loadErr := loadWorkflowOperation(ctx, tx, record.OwnerID, record.WorkflowID, record.OperationID); loadErr == nil {
			return api.ErrReleaseWorkflowOperationConflict
		} else if !errors.Is(loadErr, api.ErrReleaseWorkflowOperationNotFound) {
			return loadErr
		}
		_, insertErr := tx.ExecContext(ctx, `
			INSERT INTO release_workflow_operations (
				owner_id, workflow_id, operation_id, expected_revision, idempotency_key,
				command_fingerprint, command_name, process_epoch, status, sequence,
				operation_json, started_at, updated_at, completed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.OwnerID, record.WorkflowID, record.OperationID, record.ExpectedRevision, record.IdempotencyKey,
			record.CommandFingerprint, record.Status.Command, record.ProcessEpoch, record.Status.Status, record.Status.Sequence,
			payload, formatWorkflowStateTime(record.Status.StartedAt), formatWorkflowStateTime(record.Status.UpdatedAt), nil)
		if insertErr != nil {
			return fmt.Errorf("db create release workflow operation: %w", insertErr)
		}
		result = cloneWorkflowOperationRecord(record)
		return nil
	})
	if err != nil {
		return api.ReleaseWorkflowOperationRecord{}, false, err
	}
	return result, idempotent, nil
}

// LoadReleaseWorkflowOperation returns one exact owner-scoped operation.
func (r *SQLiteRepository) LoadReleaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowOperationRecord, error) {
	return loadWorkflowOperation(ctx, r.db, strings.TrimSpace(ownerID), workflowID, operationID)
}

// LoadReleaseWorkflowOperationByIdempotency returns one exact command receipt.
func (r *SQLiteRepository) LoadReleaseWorkflowOperationByIdempotency(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
) (api.ReleaseWorkflowOperationRecord, error) {
	return loadWorkflowOperationByIdempotency(
		ctx,
		r.db,
		strings.TrimSpace(ownerID),
		workflowID,
		strings.TrimSpace(commandName),
		strings.TrimSpace(idempotencyKey),
	)
}

// LoadLatestReleaseWorkflowOperation returns the most recently updated
// operation for one owner-scoped workflow.
func (r *SQLiteRepository) LoadLatestReleaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowOperationRecord, error) {
	return scanWorkflowOperation(r.db.QueryRowContext(ctx, workflowOperationSelect+`
		WHERE owner_id = ? AND workflow_id = ?
		ORDER BY updated_at DESC, operation_id DESC
		LIMIT 1
	`, strings.TrimSpace(ownerID), workflowID))
}

// SaveReleaseWorkflowOperation applies one optimistic sequence update without
// changing the workflow aggregate revision.
func (r *SQLiteRepository) SaveReleaseWorkflowOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	payload, err := encodeWorkflowOperationRecord(record)
	if err != nil {
		return err
	}
	if record.Status.Sequence != expectedSequence+1 {
		return errors.New("db: release workflow operation sequence must advance by one")
	}
	var completedAt any
	if record.Status.CompletedAt != nil {
		completedAt = formatWorkflowStateTime(*record.Status.CompletedAt)
	}
	return r.withWriteTx(ctx, "save release workflow operation", func(tx *sql.Tx) error {
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE release_workflow_operations
			SET process_epoch = ?, status = ?, sequence = ?, operation_json = ?, updated_at = ?, completed_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND sequence = ?
		`, record.ProcessEpoch, record.Status.Status, record.Status.Sequence, payload,
			formatWorkflowStateTime(record.Status.UpdatedAt), completedAt,
			record.OwnerID, record.WorkflowID, record.OperationID, expectedSequence)
		if updateErr != nil {
			return fmt.Errorf("db save release workflow operation: %w", updateErr)
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("db save release workflow operation rows: %w", rowsErr)
		}
		if rows == 1 {
			return nil
		}
		if _, loadErr := loadWorkflowOperation(ctx, tx, record.OwnerID, record.WorkflowID, record.OperationID); loadErr != nil {
			return loadErr
		}
		return api.ErrReleaseWorkflowOperationSequenceConflict
	})
}

// ListActiveReleaseWorkflowOperations returns queued/running operations for
// process-restart recovery.
func (r *SQLiteRepository) ListActiveReleaseWorkflowOperations(ctx context.Context) ([]api.ReleaseWorkflowOperationRecord, error) {
	rows, err := r.db.QueryContext(ctx, workflowOperationSelect+`
		WHERE status IN (?, ?)
		ORDER BY updated_at, operation_id
	`, api.StageStatusQueued, api.StageStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("db list active release workflow operations: %w", err)
	}
	defer rows.Close()
	records := make([]api.ReleaseWorkflowOperationRecord, 0)
	for rows.Next() {
		record, scanErr := scanWorkflowOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate active release workflow operations: %w", err)
	}
	return records, nil
}

// DeleteTerminalReleaseWorkflowOperationsBefore removes bounded terminal
// progress history while retaining queued and running work.
func (r *SQLiteRepository) DeleteTerminalReleaseWorkflowOperationsBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("db: release workflow operation retention cutoff is required")
	}
	result, err := r.execWrite(ctx, "delete terminal release workflow operations", `
		DELETE FROM release_workflow_operations
		WHERE status NOT IN (?, ?) AND updated_at < ?
	`, api.StageStatusQueued, api.StageStatusRunning, formatWorkflowStateTime(before))
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("db delete terminal release workflow operations rows: %w", err)
	}
	return rows, nil
}

const workflowOperationSelect = `
	SELECT owner_id, workflow_id, operation_id, expected_revision, idempotency_key,
		command_fingerprint, process_epoch, operation_json
	FROM release_workflow_operations
`

func loadWorkflowOperation(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.ReleaseWorkflowOperationRecord, error) {
	if ownerID == "" || strings.TrimSpace(string(workflowID)) == "" || strings.TrimSpace(string(operationID)) == "" {
		return api.ReleaseWorkflowOperationRecord{}, api.ErrReleaseWorkflowOperationNotFound
	}
	return scanWorkflowOperation(queryer.QueryRowContext(ctx, workflowOperationSelect+`
		WHERE owner_id = ? AND workflow_id = ? AND operation_id = ?
	`, ownerID, workflowID, operationID))
}

func loadActiveWorkflowOperation(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
) (api.ReleaseWorkflowOperationRecord, error) {
	return scanWorkflowOperation(queryer.QueryRowContext(ctx, workflowOperationSelect+`
		WHERE owner_id = ? AND workflow_id = ? AND status IN (?, ?)
		LIMIT 1
	`, ownerID, workflowID, api.StageStatusQueued, api.StageStatusRunning))
}

func loadWorkflowOperationByIdempotency(
	ctx context.Context,
	queryer workflowStateQueryer,
	ownerID string,
	workflowID api.WorkflowID,
	commandName string,
	idempotencyKey string,
) (api.ReleaseWorkflowOperationRecord, error) {
	return scanWorkflowOperation(queryer.QueryRowContext(ctx, workflowOperationSelect+`
		WHERE owner_id = ? AND workflow_id = ? AND command_name = ? AND idempotency_key = ?
		LIMIT 1
	`, ownerID, workflowID, commandName, idempotencyKey))
}

func scanWorkflowOperation(scanner workflowOperationScanner) (api.ReleaseWorkflowOperationRecord, error) {
	var record api.ReleaseWorkflowOperationRecord
	var workflowID string
	var operationID string
	var expectedRevision uint64
	var fingerprint string
	var payload []byte
	if err := scanner.Scan(
		&record.OwnerID,
		&workflowID,
		&operationID,
		&expectedRevision,
		&record.IdempotencyKey,
		&fingerprint,
		&record.ProcessEpoch,
		&payload,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReleaseWorkflowOperationRecord{}, api.ErrReleaseWorkflowOperationNotFound
		}
		return api.ReleaseWorkflowOperationRecord{}, fmt.Errorf("db load release workflow operation: %w", err)
	}
	record.WorkflowID = api.WorkflowID(workflowID)
	record.OperationID = api.WorkflowOperationID(operationID)
	record.ExpectedRevision = api.WorkflowRevision(expectedRevision)
	record.CommandFingerprint = api.WorkflowFingerprint(fingerprint)
	if err := json.Unmarshal(payload, &record.Status); err != nil {
		return api.ReleaseWorkflowOperationRecord{}, fmt.Errorf("db load release workflow operation payload: %w", err)
	}
	return cloneWorkflowOperationRecord(record), nil
}

func encodeWorkflowOperationRecord(record api.ReleaseWorkflowOperationRecord) ([]byte, error) {
	if err := validateWorkflowOperationRecord(record); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(record.Status)
	if err != nil {
		return nil, fmt.Errorf("db encode release workflow operation: %w", err)
	}
	return payload, nil
}

func validateWorkflowOperationRecord(record api.ReleaseWorkflowOperationRecord) error {
	if strings.TrimSpace(record.OwnerID) == "" || strings.TrimSpace(string(record.WorkflowID)) == "" ||
		strings.TrimSpace(string(record.OperationID)) == "" || strings.TrimSpace(record.ProcessEpoch) == "" {
		return errors.New("db: release workflow operation owner, identity, and process epoch are required")
	}
	if record.ExpectedRevision == 0 || uint64(record.ExpectedRevision) > math.MaxInt64 || record.Status.Revision != record.ExpectedRevision {
		return errors.New("db: release workflow operation revision is invalid")
	}
	if record.CommandFingerprint == "" || record.Status.ID != record.OperationID || record.Status.WorkflowID != record.WorkflowID {
		return errors.New("db: release workflow operation binding is invalid")
	}
	if err := record.Status.Validate(); err != nil {
		return fmt.Errorf("db: release workflow operation status: %w", err)
	}
	return nil
}

func cloneWorkflowOperationRecord(record api.ReleaseWorkflowOperationRecord) api.ReleaseWorkflowOperationRecord {
	status, err := record.Status.Clone()
	if err == nil {
		record.Status = status
	}
	return record
}
