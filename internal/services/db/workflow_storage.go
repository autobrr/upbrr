// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// The migration runner commits the backfill and its schema_migrations marker
// together. An interrupted upgrade retains the legacy payloads and retries the
// entire step; normal reads never infer ownership from missing backfill rows.
func migrateStructuredWorkflowStorage(ctx context.Context, exec migrationExecutor) error {
	if err := migrateWorkflowOwnership(ctx, exec); err != nil {
		return err
	}
	if err := migrateWorkflowSnapshotStorage(ctx, exec); err != nil {
		return err
	}
	var incomplete bool
	if err := exec.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM release_workflow_states s
		LEFT JOIN release_workflow_ownership o
			ON o.owner_id = s.owner_id AND o.workflow_id = s.workflow_id
		WHERE o.revision IS NULL OR o.revision <> s.revision
	)`).Scan(&incomplete); err != nil {
		return fmt.Errorf("db verify workflow backfill: %w", err)
	}
	if incomplete {
		return errors.New("db: workflow ownership backfill is incomplete")
	}
	return nil
}

// syncWorkflowStorage runs after a successful state CAS, in that same write
// transaction. Its input is the logical payload, before snapshot extraction.
func syncWorkflowStorage(ctx context.Context, exec migrationExecutor, record api.ReleaseWorkflowStateRecord) error {
	if err := syncWorkflowOwnership(ctx, exec, record); err != nil {
		return err
	}
	payload, err := saveWorkflowSnapshotStorage(ctx, exec, record)
	if err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `UPDATE release_workflow_states SET state_json = ?
		WHERE owner_id = ? AND workflow_id = ? AND revision = ?`,
		payload, record.OwnerID, record.WorkflowID, record.Revision); err != nil {
		return fmt.Errorf("db store workflow snapshot references: %w", err)
	}
	return nil
}
