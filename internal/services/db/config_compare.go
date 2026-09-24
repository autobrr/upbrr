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
	"maps"

	"github.com/autobrr/upbrr/pkg/api"
)

// SaveFullConfigIfUnchanged protects secret rewrapping from overwriting a
// configuration activated after its original encrypted snapshot was read.
func (r *SQLiteRepository) SaveFullConfigIfUnchanged(ctx context.Context, cfg any, expected json.RawMessage) error {
	return r.SaveFullConfigWithPreSave(ctx, cfg, func(ctx context.Context, tx *sql.Tx) error {
		return requireFullConfigUnchanged(ctx, tx, expected)
	})
}

func requireFullConfigUnchanged(ctx context.Context, tx *sql.Tx, expected json.RawMessage) error {
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(expected, &snapshot); err != nil {
		return errors.New("db decode expected config snapshot: invalid JSON")
	}
	expectedSections := make(map[string]string, len(snapshot))
	for section, payload := range snapshot {
		var compact bytes.Buffer
		if err := json.Compact(&compact, payload); err != nil {
			return errors.New("db compact expected config section: invalid JSON")
		}
		expectedSections[section] = compact.String()
	}
	rows, err := tx.QueryContext(ctx, `SELECT section, data FROM config_settings`)
	if err != nil {
		return fmt.Errorf("db read config snapshot for comparison: %w", err)
	}
	defer rows.Close()
	current := make(map[string]string)
	for rows.Next() {
		var section, payload string
		if err := rows.Scan(&section, &payload); err != nil {
			return fmt.Errorf("db scan config snapshot: %w", err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(payload)); err != nil {
			return errors.New("db compact current config section: invalid JSON")
		}
		current[section] = compact.String()
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db iterate config snapshot: %w", err)
	}
	if !maps.Equal(current, expectedSections) {
		return api.ErrConfigActivationChanged
	}
	return nil
}
