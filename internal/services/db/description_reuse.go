// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// migrateAddReusableDescriptions adds source-scoped, safe reusable
// description output. The JSON payload is intentionally limited by
// api.ReusableDescription to public output and user-authored override text.
func migrateAddReusableDescriptions(ctx context.Context, exec migrationExecutor) error {
	if _, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS description_reusable (
		source_path TEXT PRIMARY KEY,
		data_json TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("db: add reusable descriptions: %w", err)
	}
	return nil
}

// SaveReusableDescription replaces the safe reusable description record for
// one canonical source path.
func (r *SQLiteRepository) SaveReusableDescription(
	ctx context.Context,
	sourcePath string,
	description api.ReusableDescription,
) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	return r.withWriteTx(ctx, "save reusable description", func(tx *sql.Tx) error {
		return saveReusableDescriptionTx(ctx, tx, sourcePath, description)
	})
}

func saveReusableDescriptionTx(
	ctx context.Context,
	tx *sql.Tx,
	sourcePath string,
	description api.ReusableDescription,
) error {
	normalizedSourcePath := strings.TrimSpace(sourcePath)
	if normalizedSourcePath == "" || !description.Valid() {
		return internalerrors.ErrInvalidInput
	}
	payload, err := json.Marshal(description)
	if err != nil {
		return fmt.Errorf("db reusable description: encode: %w", err)
	}
	if err := requireReusableMediaAuthority(ctx, tx, normalizedSourcePath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO description_reusable (source_path, data_json) VALUES (?, ?)
		ON CONFLICT(source_path) DO UPDATE SET data_json = excluded.data_json
	`, normalizedSourcePath, payload); err != nil {
		return fmt.Errorf("db reusable description: save: %w", err)
	}
	return nil
}

// LoadReusableDescription returns the latest safe reusable description for one
// canonical source path. A missing record returns found=false.
func (r *SQLiteRepository) LoadReusableDescription(
	ctx context.Context,
	sourcePath string,
) (api.ReusableDescription, bool, error) {
	if r == nil || r.db == nil {
		return api.ReusableDescription{}, false, errors.New("db: repository not initialized")
	}
	normalizedSourcePath := strings.TrimSpace(sourcePath)
	if normalizedSourcePath == "" {
		return api.ReusableDescription{}, false, internalerrors.ErrInvalidInput
	}
	var payload []byte
	if err := r.db.QueryRowContext(ctx, `
		SELECT data_json FROM description_reusable WHERE source_path = ?
	`, normalizedSourcePath).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.ReusableDescription{}, false, nil
		}
		return api.ReusableDescription{}, false, fmt.Errorf("db reusable description: load: %w", err)
	}
	var description api.ReusableDescription
	if err := json.Unmarshal(payload, &description); err != nil {
		return api.ReusableDescription{}, false, fmt.Errorf("db reusable description: decode: %w", err)
	}
	if !description.Valid() {
		return api.ReusableDescription{}, false, errors.New("db reusable description: invalid stored record")
	}
	return description, true, nil
}
