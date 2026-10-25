// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"fmt"
)

// migrateImportedScreenshotEvidence adds unknown-by-default receipt storage.
// Only the atomic prepared-generation writer populates it; legacy source-cache
// writes neither manufacture nor replace prepared receipt authority.
func migrateImportedScreenshotEvidence(ctx context.Context, exec migrationExecutor) error {
	present, err := tableExists(ctx, exec, "external_metadata")
	if err != nil {
		return fmt.Errorf("db inspect imported screenshot evidence table: %w", err)
	}
	if !present {
		return nil
	}
	exists, err := tableColumnExists(ctx, exec, "external_metadata", "imported_screenshot_evidence_json")
	if err != nil {
		return fmt.Errorf("db inspect imported screenshot evidence column: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `ALTER TABLE external_metadata ADD COLUMN imported_screenshot_evidence_json TEXT NOT NULL DEFAULT ""`); err != nil {
		return fmt.Errorf("db add imported screenshot evidence: %w", err)
	}
	return nil
}
