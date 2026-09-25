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
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func migrateAddActiveInput(ctx context.Context, exec migrationExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS input_records (
			id TEXT PRIMARY KEY, canonical_path TEXT NOT NULL UNIQUE,
			source_version TEXT NOT NULL, manifest BLOB NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS active_input (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			revision INTEGER NOT NULL, fence INTEGER NOT NULL, record_json BLOB NOT NULL
		)`,
		`INSERT OR IGNORE INTO active_input (singleton, revision, fence, record_json)
		 VALUES (1, 0, 0, '{"State":"empty"}')`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db migrate active input: %w", err)
		}
	}
	return nil
}

func loadActiveInput(ctx context.Context, query workflowStateQueryer) (api.ActiveInputRecord, error) {
	var record api.ActiveInputRecord
	var payload []byte
	if err := query.QueryRowContext(ctx, `SELECT record_json FROM active_input WHERE singleton = 1`).Scan(&payload); err != nil {
		return record, fmt.Errorf("db load active input: %w", err)
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		return record, fmt.Errorf("db decode active input: %w", err)
	}
	return record, nil
}

// LoadActiveInput reads the slot without changing coordinator ownership.
func (r *SQLiteRepository) LoadActiveInput(ctx context.Context) (api.ActiveInputRecord, error) {
	return loadActiveInput(ctx, r.historyQuery(ctx))
}

// CompareAndSwapActiveInput commits a transition only for its exact revision and live fence.
// An expired coordinator can only be replaced by an explicit recovery transition.
func (r *SQLiteRepository) CompareAndSwapActiveInput(
	ctx context.Context, expected, next api.ActiveInputRecord, now time.Time,
) error {
	return r.transitionActiveInput(ctx, expected, next, nil, nil, now)
}

// CloseIdleActiveInput atomically closes one exact committed input without
// requiring its prior coordinator to still hold a live lease. It is limited
// to idle active records so a restarted process cannot clear in-flight work.
// Unresolved effects remain durable and continue to block a fresh opening
// until explicit reconciliation resolves them.
func (r *SQLiteRepository) CloseIdleActiveInput(ctx context.Context, expected api.ActiveInputRecord, now time.Time) error {
	if expected.State != api.ActiveInputActive || expected.Revision >= math.MaxInt64 {
		return api.ErrActiveInputChanged
	}
	now = now.UTC()
	return r.withWriteTx(ctx, "close idle active input", func(tx *sql.Tx) error {
		current, err := loadActiveInput(ctx, tx)
		if err != nil {
			return err
		}
		if current.State != api.ActiveInputActive || current.Revision != expected.Revision || current.Fence != expected.Fence ||
			current.OwnerID != expected.OwnerID || current.CoordinatorID != expected.CoordinatorID {
			return api.ErrActiveInputChanged
		}
		var running int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_operations
			WHERE status IN ('queued', 'running'))`).Scan(&running); err != nil {
			return fmt.Errorf("db inspect idle active input operations: %w", err)
		}
		if running != 0 {
			return api.ErrActiveInputBusy
		}
		var leased int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_work
			WHERE completed_at IS NULL AND lease_expires_at > ?)`, formatWorkflowStateTime(now)).Scan(&leased); err != nil {
			return fmt.Errorf("db inspect idle active input work leases: %w", err)
		}
		if leased != 0 {
			return api.ErrActiveInputBusy
		}
		empty := api.ActiveInputRecord{
			State:          api.ActiveInputEmpty,
			Revision:       current.Revision + 1,
			Fence:          current.Fence,
			OwnerID:        current.OwnerID,
			CoordinatorID:  current.CoordinatorID,
			LeaseExpiresAt: current.LeaseExpiresAt,
		}
		return writeActiveInput(ctx, tx, empty)
	})
}

// FinalizeActiveInput atomically publishes verified input, workflow invalidation, and the slot.
func (r *SQLiteRepository) FinalizeActiveInput(ctx context.Context, expected, next api.ActiveInputRecord,
	input api.InputRecord, workflow *api.ReleaseWorkflowStateRecord, now time.Time,
) error {
	if next.State != api.ActiveInputActive || input.ID != next.InputID || input.SourceVersion != next.SourceVersion {
		return api.ErrActiveInputChanged
	}
	if input.CanonicalPath == "" || len(input.Manifest) == 0 || input.UpdatedAt.IsZero() {
		return api.ErrActiveInputChanged
	}
	return r.transitionActiveInput(ctx, expected, next, &input, workflow, now)
}

func (r *SQLiteRepository) transitionActiveInput(ctx context.Context, expected, next api.ActiveInputRecord,
	input *api.InputRecord, workflow *api.ReleaseWorkflowStateRecord, now time.Time,
) error {
	if expected.Revision >= math.MaxInt64 || expected.Fence >= math.MaxInt64 || next.Revision != expected.Revision+1 {
		return api.ErrActiveInputChanged
	}
	return r.withWriteTx(ctx, "transition active input", func(tx *sql.Tx) error {
		current, err := loadActiveInput(ctx, tx)
		if err != nil {
			return err
		}
		if current.Revision != expected.Revision || current.Fence != expected.Fence || current.CoordinatorID != expected.CoordinatorID {
			return api.ErrActiveInputChanged
		}
		acquiring := current.State == api.ActiveInputEmpty || !current.LeaseExpiresAt.After(now)
		legacyRecoveryAcquire := current.State == api.ActiveInputEmpty && next.State == api.ActiveInputRecovering
		if acquiring {
			if next.Fence != current.Fence+1 || next.CoordinatorID == "" || next.OwnerID == "" || !next.LeaseExpiresAt.After(now) {
				return api.ErrActiveInputLeaseLost
			}
			if current.State != api.ActiveInputEmpty && next.State != api.ActiveInputRecovering {
				return api.ErrActiveInputLeaseLost
			}
			if current.State == api.ActiveInputEmpty && next.State != api.ActiveInputOpening && !legacyRecoveryAcquire {
				return api.ErrActiveInputChanged
			}
		} else if next.Fence != current.Fence || next.CoordinatorID != current.CoordinatorID || next.OwnerID != current.OwnerID {
			return api.ErrActiveInputBusy
		}
		if legacyRecoveryAcquire {
			if next.InputID != "" || next.SourceVersion != "" || next.WorkflowID == "" ||
				next.ReservationID != "" || next.RequestedPath != "" || next.IdempotencyKey != "" {
				return api.ErrActiveInputChanged
			}
			if _, err := loadWorkflowState(ctx, tx, next.OwnerID, next.WorkflowID); err != nil {
				return err
			}
			var unresolved int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM release_workflow_effects
				WHERE owner_id = ? AND workflow_id = ? AND status IN ('started', 'unknown')
			)`, next.OwnerID, next.WorkflowID).Scan(&unresolved); err != nil {
				return fmt.Errorf("db inspect legacy recovery effects: %w", err)
			}
			if unresolved == 0 {
				return api.ErrActiveInputChanged
			}
		} else if err := validateActiveInputTransition(current, next, acquiring); err != nil {
			return err
		}
		restoringOwnedInput := current.State == api.ActiveInputRecovering && next.State == api.ActiveInputActive &&
			next.InputID == current.InputID && next.WorkflowID == current.WorkflowID && next.SourceVersion == current.SourceVersion
		legacyRecoveryClose := current.State == api.ActiveInputRecovering && current.InputID == "" && current.SourceVersion == "" &&
			current.WorkflowID != "" && next.State == api.ActiveInputEmpty
		if next.State != api.ActiveInputRecovering && !restoringOwnedInput && !legacyRecoveryClose {
			var unresolved int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_effects
				WHERE status IN ('started', 'unknown'))`).Scan(&unresolved); err != nil {
				return fmt.Errorf("db inspect active input effects: %w", err)
			}
			if unresolved != 0 {
				return api.ErrReleaseWorkflowEffectOutcomeUnknown
			}
			if next.State != api.ActiveInputSwitchPending {
				var running int
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_operations
					WHERE status IN ('queued', 'running'))`).Scan(&running); err != nil {
					return fmt.Errorf("db inspect active input work: %w", err)
				}
				if running != 0 {
					return api.ErrActiveInputBusy
				}
			}
		}
		if input != nil {
			saved, err := saveInputRecord(ctx, tx, *input)
			if err != nil {
				return err
			}
			if saved.ID != next.InputID {
				return api.ErrActiveInputChanged
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO input_workflow_associations
				(canonical_path, owner_id, source_version, workflow_id, audio_analysis_id, updated_at)
				VALUES (?, ?, ?, ?, '', ?)
				ON CONFLICT(canonical_path, owner_id) DO UPDATE SET
					source_version = excluded.source_version,
					workflow_id = excluded.workflow_id,
					audio_analysis_id = excluded.audio_analysis_id,
					updated_at = excluded.updated_at`,
				saved.CanonicalPath, next.OwnerID, saved.SourceVersion, next.WorkflowID, formatWorkflowStateTime(now)); err != nil {
				return fmt.Errorf("db associate input workflow: %w", err)
			}
		}
		if workflow != nil {
			if workflow.OwnerID != next.OwnerID || workflow.WorkflowID != next.WorkflowID || workflow.Revision == 0 {
				return api.ErrActiveInputChanged
			}
			result, err := tx.ExecContext(ctx, `UPDATE release_workflow_states SET revision = ?, status = ?, state_json = ?, updated_at = ?
				WHERE owner_id = ? AND workflow_id = ? AND revision = ?`, workflow.Revision, workflow.Status, workflow.Payload,
				formatWorkflowStateTime(workflow.UpdatedAt), workflow.OwnerID, workflow.WorkflowID, workflow.Revision-1)
			if err != nil {
				return fmt.Errorf("db finalize active workflow: %w", err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("db finalize active workflow rows: %w", err)
			}
			if rows != 1 {
				return api.ErrReleaseWorkflowRevisionConflict
			}
		}
		return writeActiveInput(ctx, tx, next)
	})
}

// requireActiveInputFence is called inside the same write transaction as admission.
// A token read before a takeover cannot authorize a later external effect.
func requireActiveInputFence(
	ctx context.Context, tx *sql.Tx, owner string, workflow api.WorkflowID, coordinator string, fence uint64, now time.Time,
) error {
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return err
	}
	if slot.State != api.ActiveInputActive || slot.OwnerID != owner || slot.WorkflowID != workflow ||
		slot.CoordinatorID != coordinator || slot.Fence != fence || !slot.LeaseExpiresAt.After(now) {
		return api.ErrActiveInputLeaseLost
	}
	return nil
}

func requireWorkflowInputMutation(ctx context.Context, tx *sql.Tx, owner string, workflow api.WorkflowID, allowPending bool) error {
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return err
	}
	// Existing pre-upgrade rows may be reconciled before the first explicit open.
	if slot.Fence == 0 {
		return nil
	}
	authority, ok := api.ActiveInputAuthorityFromContext(ctx)
	if !ok || authority.CoordinatorID != slot.CoordinatorID || authority.Fence != slot.Fence ||
		owner != slot.OwnerID || !slot.LeaseExpiresAt.After(time.Now().UTC()) {
		return api.ErrActiveInputLeaseLost
	}
	if slot.State == api.ActiveInputActive && slot.WorkflowID == workflow {
		return nil
	}
	if allowPending && (slot.State == api.ActiveInputSwitchPending || slot.State == api.ActiveInputRecovering) && slot.WorkflowID == workflow {
		return nil
	}
	return api.ErrActiveInputChanged
}

func requireLegacyInputRecovery(ctx context.Context, tx *sql.Tx, owner string, workflow api.WorkflowID) error {
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return err
	}
	authority, ok := api.ActiveInputAuthorityFromContext(ctx)
	if !ok || slot.State != api.ActiveInputRecovering || slot.InputID != "" || slot.SourceVersion != "" ||
		slot.OwnerID != owner || slot.WorkflowID != workflow || slot.CoordinatorID != authority.CoordinatorID ||
		slot.Fence != authority.Fence || !slot.LeaseExpiresAt.After(time.Now().UTC()) {
		return api.ErrActiveInputLeaseLost
	}
	return nil
}

func (r *SQLiteRepository) execWorkflowInputWrite(
	ctx context.Context,
	operation, owner string,
	workflow api.WorkflowID,
	query string,
	args ...any,
) (sql.Result, error) {
	var result sql.Result
	err := r.withWriteTx(ctx, operation, func(tx *sql.Tx) error {
		if err := requireWorkflowInputMutation(ctx, tx, owner, workflow, true); err != nil {
			return err
		}
		var err error
		result, err = tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("db %s: %w", operation, err)
		}
		return nil
	})
	return result, err
}

func validateActiveInputTransition(current, next api.ActiveInputRecord, acquiring bool) error {
	if next.State == api.ActiveInputRecovering && acquiring {
		if next.InputID != current.InputID || next.WorkflowID != current.WorkflowID || next.SourceVersion != current.SourceVersion ||
			next.ReservationID != current.ReservationID || next.RequestedPath != current.RequestedPath {
			return api.ErrActiveInputChanged
		}
		return nil
	}
	switch next.State {
	case api.ActiveInputRecovering:
		return api.ErrActiveInputChanged
	case api.ActiveInputOpening, api.ActiveInputSwitchPending:
		if next.ReservationID == "" || next.RequestedPath == "" || next.IdempotencyKey == "" ||
			next.InputID != current.InputID || next.WorkflowID != current.WorkflowID || next.SourceVersion != current.SourceVersion {
			return api.ErrActiveInputChanged
		}
		if next.State == api.ActiveInputOpening && current.State != api.ActiveInputEmpty ||
			next.State == api.ActiveInputSwitchPending && current.State != api.ActiveInputActive {
			return api.ErrActiveInputChanged
		}
	case api.ActiveInputActive:
		if next.InputID == "" || next.SourceVersion == "" || next.WorkflowID == "" || next.ReservationID != "" || next.RequestedPath != "" {
			return api.ErrActiveInputChanged
		}
	case api.ActiveInputEmpty:
		if next.InputID != "" || next.SourceVersion != "" || next.WorkflowID != "" || next.ReservationID != "" || next.RequestedPath != "" {
			return api.ErrActiveInputChanged
		}
	default:
		return api.ErrActiveInputChanged
	}
	return nil
}

func writeActiveInput(ctx context.Context, tx *sql.Tx, record api.ActiveInputRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("db encode active input: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE active_input SET revision = ?, fence = ?, record_json = ? WHERE singleton = 1`,
		record.Revision, record.Fence, payload); err != nil {
		return fmt.Errorf("db update active input: %w", err)
	}
	return nil
}

// RenewActiveInput cannot resurrect an expired coordinator or renew a replacement's fence.
func (r *SQLiteRepository) RenewActiveInput(ctx context.Context, coordinator string, fence uint64, now, expires time.Time) error {
	return r.withWriteTx(ctx, "renew active input", func(tx *sql.Tx) error {
		current, err := loadActiveInput(ctx, tx)
		if err != nil {
			return err
		}
		if current.State == api.ActiveInputEmpty || current.CoordinatorID != coordinator || current.Fence != fence ||
			!current.LeaseExpiresAt.After(now) || !expires.After(current.LeaseExpiresAt) {
			return api.ErrActiveInputLeaseLost
		}
		current.LeaseExpiresAt = expires
		return writeActiveInput(ctx, tx, current)
	})
}

// RelinquishActiveInput expires only the current coordinator's live lease. It
// preserves the committed input so a successor can recover it under a new fence.
func (r *SQLiteRepository) RelinquishActiveInput(ctx context.Context, coordinator string, fence uint64, now time.Time) error {
	return r.withWriteTx(ctx, "relinquish active input", func(tx *sql.Tx) error {
		current, err := loadActiveInput(ctx, tx)
		if err != nil {
			return err
		}
		if current.State == api.ActiveInputEmpty || current.CoordinatorID != coordinator || current.Fence != fence ||
			!current.LeaseExpiresAt.After(now) {
			return api.ErrActiveInputLeaseLost
		}
		current.LeaseExpiresAt = now.UTC()
		return writeActiveInput(ctx, tx, current)
	})
}

// LoadInputRecordByID resolves a stable input ID to its input record.
func (r *SQLiteRepository) LoadInputRecordByID(ctx context.Context, id string) (api.InputRecord, error) {
	var source string
	if err := r.historyQuery(ctx).QueryRowContext(ctx, `SELECT canonical_path FROM input_records WHERE id = ?`, id).Scan(&source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.InputRecord{}, api.ErrInputRecordNotFound
		}
		return api.InputRecord{}, fmt.Errorf("db resolve input id: %w", err)
	}
	return r.LoadInputRecord(ctx, source)
}

// LoadInputRecord resolves a canonical source path to its stable input record.
func (r *SQLiteRepository) LoadInputRecord(ctx context.Context, canonicalPath string) (api.InputRecord, error) {
	var record api.InputRecord
	var updated string
	err := r.historyQuery(ctx).QueryRowContext(ctx, `SELECT id, canonical_path, source_version, manifest, updated_at
		FROM input_records WHERE canonical_path = ?`, canonicalPath).Scan(
		&record.ID, &record.CanonicalPath, &record.SourceVersion, &record.Manifest, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, api.ErrInputRecordNotFound
		}
		return record, fmt.Errorf("db load input record: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return record, fmt.Errorf("db parse input update: %w", err)
	}
	return record, nil
}

// LoadInputWorkflowAssociation resolves the last workflow for this owner and verified source version.
func (r *SQLiteRepository) LoadInputWorkflowAssociation(ctx context.Context, canonicalPath, ownerID, sourceVersion string) (
	api.WorkflowID, api.AudioAnalysisResultID, error,
) {
	var workflowID api.WorkflowID
	var audioID api.AudioAnalysisResultID
	err := r.historyQuery(ctx).QueryRowContext(ctx, `SELECT workflow_id, audio_analysis_id FROM input_workflow_associations
		WHERE canonical_path = ? AND owner_id = ? AND source_version = ?`, canonicalPath, ownerID, sourceVersion).Scan(&workflowID, &audioID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("db load input workflow association: %w", err)
	}
	return workflowID, audioID, nil
}

// SaveInputRecord preserves the original opaque ID when the same path is inspected again.
func (r *SQLiteRepository) SaveInputRecord(ctx context.Context, record api.InputRecord) (api.InputRecord, error) {
	if record.ID == "" || record.CanonicalPath == "" || record.SourceVersion == "" || len(record.Manifest) == 0 || record.UpdatedAt.IsZero() {
		return api.InputRecord{}, errors.New("db: incomplete input record")
	}
	err := r.withWriteTx(ctx, "save input record", func(tx *sql.Tx) error {
		var err error
		record, err = saveInputRecord(ctx, tx, record)
		return err
	})
	return record, err
}

func saveInputRecord(ctx context.Context, tx *sql.Tx, record api.InputRecord) (api.InputRecord, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO input_records (id, canonical_path, source_version, manifest, updated_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(canonical_path) DO UPDATE SET
			source_version = excluded.source_version, manifest = excluded.manifest,
			updated_at = excluded.updated_at`,
		record.ID, record.CanonicalPath, record.SourceVersion, record.Manifest, formatWorkflowStateTime(record.UpdatedAt))
	if err != nil {
		return api.InputRecord{}, fmt.Errorf("db save input record: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM input_records WHERE canonical_path = ?`, record.CanonicalPath).
		Scan(&record.ID); err != nil {
		return api.InputRecord{}, fmt.Errorf("db resolve input record: %w", err)
	}
	return record, nil
}
