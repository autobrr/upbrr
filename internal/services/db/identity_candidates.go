// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"fmt"
)

// migrateIdentityCandidates retains advisory matches only through the atomic
// prepared-generation writer; legacy source-cache writes leave them unchanged.
func migrateIdentityCandidates(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableColumnExists(ctx, exec, "external_metadata", "identity_candidates_json")
	if err != nil {
		return fmt.Errorf("db inspect identity candidates column: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `ALTER TABLE external_metadata ADD COLUMN identity_candidates_json TEXT NOT NULL DEFAULT ""`); err != nil {
		return fmt.Errorf("db add identity candidates: %w", err)
	}
	return nil
}
