// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"fmt"
)

// migrateAddSubmissionFences creates the owner-independent tracker submission
// fence. It deliberately performs no legacy history backfill because old
// path-level records cannot prove the exact canonical submitted-content scope.
func migrateAddSubmissionFences(ctx context.Context, exec migrationExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS submission_fences (
			content_version TEXT NOT NULL,
			content_digest TEXT NOT NULL,
			content_scope TEXT NOT NULL,
			content_json BLOB NOT NULL,
			tracker_site TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			operation_id TEXT NOT NULL,
			effect_id TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			confirmed_at TEXT,
			PRIMARY KEY (content_version, content_digest, tracker_site)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_submission_fences_effect
			ON submission_fences (owner_id, workflow_id, operation_id, effect_id, status)`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: add submission fences: %w", err)
		}
	}
	return nil
}
