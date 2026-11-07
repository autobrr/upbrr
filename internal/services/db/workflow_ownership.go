// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

var errWorkflowOwnershipUncertain = errors.New("db: workflow ownership is incomplete or ambiguous; retain history and repair the workflow before cleanup")

// migrateWorkflowOwnership runs inside the migration transaction. Its migration
// ID is recorded only after every retained workflow has a classification at its
// exact revision. Cancellation rolls back both the backfill and its boundary.
func migrateWorkflowOwnership(ctx context.Context, exec migrationExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS release_workflow_ownership (
			owner_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			ambiguous INTEGER NOT NULL,
			empty INTEGER NOT NULL,
			PRIMARY KEY (owner_id, workflow_id),
			FOREIGN KEY (owner_id, workflow_id) REFERENCES release_workflow_states (owner_id, workflow_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS release_workflow_sources (
			owner_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			source_path TEXT NOT NULL,
			PRIMARY KEY (owner_id, workflow_id, source_path),
			FOREIGN KEY (owner_id, workflow_id) REFERENCES release_workflow_states (owner_id, workflow_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_release_workflow_sources_path ON release_workflow_sources (source_path, owner_id, workflow_id)`,
		`CREATE INDEX IF NOT EXISTS idx_input_workflow_associations_workflow ON input_workflow_associations (owner_id, workflow_id)`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db migrate workflow ownership: %w", err)
		}
	}

	// Read one payload at a time; retained histories need not fit in memory.
	var ownerID string
	var workflowID api.WorkflowID
	for {
		var record api.ReleaseWorkflowStateRecord
		err := exec.QueryRowContext(ctx, `SELECT owner_id, workflow_id, revision, status, state_json
			FROM release_workflow_states
			WHERE (owner_id, workflow_id) > (?, ?)
			ORDER BY owner_id, workflow_id LIMIT 1`, ownerID, workflowID).
			Scan(&record.OwnerID, &record.WorkflowID, &record.Revision, &record.Status, &record.Payload)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return fmt.Errorf("db migrate workflow ownership read: %w", err)
		}
		record, err = loadWorkflowSnapshotStorage(ctx, exec, record)
		if err != nil {
			return fmt.Errorf("db migrate workflow ownership restore: %w", err)
		}
		if err := syncWorkflowOwnership(ctx, exec, record); err != nil {
			return err
		}
		ownerID, workflowID = record.OwnerID, record.WorkflowID
	}
	var missing int
	if err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM release_workflow_states AS state
		LEFT JOIN release_workflow_ownership AS ownership USING (owner_id, workflow_id)
		WHERE ownership.revision IS NULL OR ownership.revision <> state.revision`).Scan(&missing); err != nil {
		return fmt.Errorf("db migrate workflow ownership verify: %w", err)
	}
	if missing != 0 {
		return errWorkflowOwnershipUncertain
	}
	return nil
}

// syncWorkflowOwnership replaces the independently queried source membership
// in the same transaction as its workflow revision. Malformed retained payloads
// stay recoverable and block cleanup instead of acquiring invented ownership.
func syncWorkflowOwnership(ctx context.Context, exec migrationExecutor, record api.ReleaseWorkflowStateRecord) error {
	paths, ambiguous, empty := classifyOrphanHistoryWorkflow(record.Payload)
	rows, err := exec.QueryContext(ctx, `SELECT canonical_path FROM input_workflow_associations WHERE owner_id = ? AND workflow_id = ?`,
		record.OwnerID, record.WorkflowID)
	if err != nil {
		return fmt.Errorf("db workflow ownership associations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return fmt.Errorf("db workflow ownership association: %w", err)
		}
		paths = addOrphanHistoryPath(paths, path)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("db workflow ownership associations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db workflow ownership associations close: %w", err)
	}
	if len(paths) > 0 {
		empty = false
	}
	if empty && record.Status != api.WorkflowStatusDraft && record.Status != api.WorkflowStatusBlocked {
		ambiguous = true
	}
	if _, err := exec.ExecContext(ctx, `INSERT INTO release_workflow_ownership (owner_id, workflow_id, revision, ambiguous, empty)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (owner_id, workflow_id) DO UPDATE SET
		revision = excluded.revision, ambiguous = excluded.ambiguous, empty = excluded.empty`,
		record.OwnerID, record.WorkflowID, record.Revision, ambiguous, empty); err != nil {
		return fmt.Errorf("db workflow ownership classification: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM release_workflow_sources WHERE owner_id = ? AND workflow_id = ?`,
		record.OwnerID, record.WorkflowID); err != nil {
		return fmt.Errorf("db workflow ownership replace sources: %w", err)
	}
	for _, path := range paths {
		if _, err := exec.ExecContext(ctx, `INSERT INTO release_workflow_sources (owner_id, workflow_id, source_path) VALUES (?, ?, ?)`,
			record.OwnerID, record.WorkflowID, path); err != nil {
			return fmt.Errorf("db workflow ownership source: %w", err)
		}
	}
	return nil
}

func requireWorkflowOwnershipCertain(ctx context.Context, query workflowStateQueryer) error {
	var uncertain bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_states AS state
		LEFT JOIN release_workflow_ownership AS ownership USING (owner_id, workflow_id)
		WHERE ownership.revision IS NULL OR ownership.revision <> state.revision OR ownership.ambiguous <> 0)`).Scan(&uncertain); err != nil {
		return fmt.Errorf("db inspect workflow ownership: %w", err)
	}
	if uncertain {
		return errWorkflowOwnershipUncertain
	}
	return nil
}

// workflowOwnershipSource decodes only source identity. Snapshot contents are
// skipped by encoding/json and are never hydrated merely to answer ownership.
type workflowOwnershipSource struct {
	SourcePath       string
	PreparationInput *struct{ SourcePath string }
	Releases         map[string]struct {
		Release struct {
			Source struct{ SourcePath string }
		}
	}
	Composite *struct {
		Intent struct {
			Preparation *struct{ SourcePath string }
		}
	}
}

func decodeWorkflowOwnership(payload []byte) (workflowOwnershipSource, []string, error) {
	var state workflowOwnershipSource
	if trimmed := bytes.TrimSpace(payload); len(trimmed) == 0 || trimmed[0] != '{' {
		return state, nil, errWorkflowOwnershipUncertain
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return state, nil, errWorkflowOwnershipUncertain
	}
	paths := make([]string, 0, len(state.Releases)+3)
	paths = addOrphanHistoryPath(paths, state.SourcePath)
	if state.PreparationInput != nil {
		paths = addOrphanHistoryPath(paths, state.PreparationInput.SourcePath)
	}
	if state.Composite != nil && state.Composite.Intent.Preparation != nil {
		paths = addOrphanHistoryPath(paths, state.Composite.Intent.Preparation.SourcePath)
	}
	for _, release := range state.Releases {
		paths = addOrphanHistoryPath(paths, release.Release.Source.SourcePath)
	}
	return state, paths, nil
}
