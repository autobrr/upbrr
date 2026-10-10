// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// CheckpointReleaseWorkflowResources saves a private resource registry at the current intent revision.
// The workflow adapter merges resources into the stored state before this call.
func (r *SQLiteRepository) CheckpointReleaseWorkflowResources(ctx context.Context, record api.ReleaseWorkflowStateRecord) error {
	if err := validateWorkflowStateRecord(record); err != nil {
		return err
	}
	return r.withWriteTx(ctx, "checkpoint HDR extraction resources", func(tx *sql.Tx) error {
		if err := requireWorkflowInputMutation(ctx, tx, record.OwnerID, record.WorkflowID, true); err != nil {
			return err
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE release_workflow_states SET state_json = ? WHERE owner_id = ? AND workflow_id = ? AND revision = ?`,
			record.Payload,
			record.OwnerID,
			record.WorkflowID,
			record.Revision,
		)
		if err != nil {
			return fmt.Errorf("db checkpoint HDR resources: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db checkpoint HDR resources rows: %w", err)
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
