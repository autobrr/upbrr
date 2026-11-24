// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// InputLogAttempt locates an input's shared log records without storing their contents.
type InputLogAttempt struct {
	ID, SourcePath, RequestedPath, Kind, Outcome string
	StartedAt                                    time.Time
}

func migrateInputLogs(ctx context.Context, exec migrationExecutor) error {
	if _, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS input_log_attempts (
		id TEXT PRIMARY KEY, source_path TEXT NOT NULL, requested_path TEXT NOT NULL, kind TEXT NOT NULL,
		started_at TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("create input log association table: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_input_log_source ON input_log_attempts(source_path, started_at DESC)`); err != nil {
		return fmt.Errorf("index input log association table: %w", err)
	}
	return nil
}

// BeginInputLog persists the association before the attempt emits any scoped entries.
func (r *SQLiteRepository) BeginInputLog(ctx context.Context, a InputLogAttempt) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	sourcePath := cmp.Or(a.SourcePath, a.RequestedPath)
	requestedPath := cmp.Or(a.RequestedPath, a.SourcePath)
	if _, err := r.execWrite(ctx, "save input log association", `INSERT INTO input_log_attempts (id, source_path, requested_path, kind, started_at)
		VALUES (?, ?, ?, ?, ?)`, a.ID, sourcePath, requestedPath, a.Kind, formatWorkflowStateTime(a.StartedAt)); err != nil {
		return err
	}
	return nil
}

// FinishInputLog records the observed outcome and the verified path, when available.
// If the row was removed while an owned capture was still running, it atomically
// recreates the association using the attempt's original identity, requested path,
// kind, and start time alongside the verified path and outcome.
func (r *SQLiteRepository) FinishInputLog(ctx context.Context, a InputLogAttempt) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	sourcePath := cmp.Or(a.SourcePath, a.RequestedPath)
	requestedPath := cmp.Or(a.RequestedPath, a.SourcePath)
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(requestedPath) == "" ||
		strings.TrimSpace(a.Kind) == "" || a.StartedAt.IsZero() {
		return internalerrors.ErrInvalidInput
	}
	if _, err := r.execWrite(ctx, "save input log outcome", `INSERT INTO input_log_attempts (id, source_path, requested_path, kind, started_at, outcome)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source_path = coalesce(nullif(?, ''), input_log_attempts.source_path),
			outcome = excluded.outcome`,
		a.ID, sourcePath, requestedPath, a.Kind, formatWorkflowStateTime(a.StartedAt), a.Outcome, a.SourcePath); err != nil {
		return err
	}
	return nil
}

// InputLogs lists attempts for a stored path, or finds one exact attempt by its opaque ID.
func (r *SQLiteRepository) InputLogs(ctx context.Context, source, id string) ([]InputLogAttempt, error) {
	if id != "" || r.historyTransaction(ctx) != nil {
		return queryInputLogs(ctx, r.historyQuery(ctx), source, id, false)
	}
	return readInputLogSnapshot(ctx, r.db, source, false)
}

func queryInputLogs(ctx context.Context, query historyQueryer, source, id string, includeRequested bool) ([]InputLogAttempt, error) {
	filter, args := "id = ?", []any{id}
	if id == "" {
		paths, err := inputLogPathAliases(ctx, query, source, includeRequested)
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return []InputLogAttempt{}, nil
		}
		encoded, err := json.Marshal(paths)
		if err != nil {
			return nil, fmt.Errorf("encode input log aliases: %w", err)
		}
		filter, args = "source_path IN (SELECT value FROM json_each(?))", []any{string(encoded)}
		if includeRequested {
			filter += " OR requested_path IN (SELECT value FROM json_each(?))"
			args = append(args, string(encoded))
		}
	}
	rows, err := query.QueryContext(ctx, `SELECT id, source_path, requested_path, kind, outcome, started_at FROM input_log_attempts
		WHERE `+filter+` ORDER BY rtrim(started_at,'Z') DESC, rowid DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("db list input logs: %w", err)
	}
	defer rows.Close()
	result := make([]InputLogAttempt, 0)
	for rows.Next() {
		var a InputLogAttempt
		var stamp string
		if err := rows.Scan(&a.ID, &a.SourcePath, &a.RequestedPath, &a.Kind, &a.Outcome, &stamp); err != nil {
			return nil, fmt.Errorf("read input log association: %w", err)
		}
		a.StartedAt, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, fmt.Errorf("parse input log start time: %w", err)
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate input log associations: %w", err)
	}
	return result, nil
}

// readInputLogSnapshot keeps path aliases and attempts on the same SQLite snapshot.
func readInputLogSnapshot(ctx context.Context, conn *sql.DB, source string, includeRequested bool) ([]InputLogAttempt, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin input log snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return queryInputLogs(ctx, tx, source, "", includeRequested)
}

// ReadOnlyInputLogs reads retained associations without bootstrapping an app runtime.
func ReadOnlyInputLogs(ctx context.Context, dbPath, source string) ([]InputLogAttempt, error) {
	return readOnlyInputLogs(ctx, dbPath, source, nil)
}

func readOnlyInputLogs(ctx context.Context, dbPath, source string, logger Logger) ([]InputLogAttempt, error) {
	if ctx == nil {
		return nil, errors.New("db: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read input logs canceled: %w", err)
	}
	uriPath := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: "mode=ro",
	}
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open input log database read-only: %w", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	var attempts []InputLogAttempt
	err = retryBusyContext(ctx, logger, "read input logs", func() error {
		var available bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='input_log_attempts')`).
			Scan(&available); err != nil {
			return fmt.Errorf("inspect input log schema: %w", err)
		}
		if !available {
			attempts = nil
			return nil
		}
		loaded, err := readInputLogSnapshot(ctx, conn, source, true)
		if err != nil {
			return err
		}
		attempts = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempts, nil
}

// InputLogSources returns up to 100 recent log-only inputs in recency order,
// excluding equivalent paths already displayed by regular History.
func (r *SQLiteRepository) InputLogSources(ctx context.Context, storedPaths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(storedPaths))
	for _, p := range storedPaths {
		seen[pathing.LexicalPathKey(p)] = struct{}{}
	}
	rows, err := r.historyQuery(ctx).QueryContext(ctx, `SELECT source_path FROM input_log_attempts
		GROUP BY source_path ORDER BY max(rtrim(started_at, 'Z')) DESC, max(rowid) DESC, source_path`)
	if err != nil {
		return nil, fmt.Errorf("list input log sources: %w", err)
	}
	defer rows.Close()
	paths := make([]string, 0)
	for len(paths) < 100 && rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("read input log source: %w", err)
		}
		key := pathing.LexicalPathKey(path)
		if _, duplicate := seen[key]; key != "" && !duplicate {
			seen[key] = struct{}{}
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate input log sources: %w", err)
	}
	return paths, nil
}

func inputLogSourceAliases(ctx context.Context, query historyQueryer, source string) ([]string, error) {
	return inputLogPathAliases(ctx, query, source, false)
}

// DeleteInputLogs removes input log associations matching source by host
// lexical path equivalence without expanding symlinks or directory descendants
// and without modifying stored release metadata, upload records, workflow
// state, or local files.
func (r *SQLiteRepository) DeleteInputLogs(ctx context.Context, source string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db delete input logs canceled: %w", err)
	}
	targetKey := pathing.LexicalPathKey(source)
	if targetKey == "" {
		return internalerrors.ErrInvalidInput
	}
	return r.withWriteTx(ctx, "delete input logs", func(tx *sql.Tx) error {
		if err := rejectActiveInputLogDelete(ctx, tx, targetKey); err != nil {
			return err
		}
		logPaths, err := inputLogSourceAliases(ctx, tx, source)
		if err != nil {
			return err
		}
		for _, logPath := range logPaths {
			if _, err := tx.ExecContext(ctx, `DELETE FROM input_log_attempts WHERE source_path = ?`, logPath); err != nil {
				return fmt.Errorf("db delete input logs: %w", err)
			}
		}
		return nil
	})
}

func rejectActiveInputLogDelete(ctx context.Context, query workflowStateQueryer, targetKey string) error {
	slot, err := loadActiveInput(ctx, query)
	if err != nil {
		return err
	}
	if slot.State == api.ActiveInputEmpty {
		return nil
	}
	activePaths := []string{slot.RequestedPath}
	if strings.TrimSpace(slot.InputID) != "" {
		var canonicalPath string
		err := query.QueryRowContext(ctx, `SELECT canonical_path FROM input_records WHERE id = ?`, slot.InputID).Scan(&canonicalPath)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return api.ErrActiveInputBusy
			}
			return fmt.Errorf("db delete input logs: active input path: %w", err)
		}
		activePaths = append(activePaths, canonicalPath)
	}
	active := false
	for _, activePath := range activePaths {
		if key := pathing.LexicalPathKey(activePath); key != "" && key == targetKey {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	if slot.WorkflowID != "" {
		var unresolved int
		if err := query.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM release_workflow_effects
			WHERE owner_id = ? AND workflow_id = ? AND status IN ('started', 'unknown'))`, slot.OwnerID, slot.WorkflowID).Scan(&unresolved); err != nil {
			return fmt.Errorf("db delete input logs: inspect active input effects: %w", err)
		}
		if unresolved != 0 {
			return api.ErrReleaseWorkflowEffectOutcomeUnknown
		}
	}
	return api.ErrActiveInputBusy
}

func inputLogPathAliases(ctx context.Context, query historyQueryer, source string, includeRequested bool) ([]string, error) {
	key := pathing.LexicalPathKey(source)
	if key == "" {
		return nil, nil
	}
	stmt := `SELECT DISTINCT source_path FROM input_log_attempts`
	if includeRequested {
		stmt = `SELECT source_path FROM input_log_attempts UNION SELECT requested_path FROM input_log_attempts`
	}
	rows, err := query.QueryContext(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("list input log aliases: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("read input log alias: %w", err)
		}
		if key == pathing.LexicalPathKey(path) {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate input log aliases: %w", err)
	}
	return paths, nil
}
