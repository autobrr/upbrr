// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/pathing"
)

// InputLogAttempt locates an input's shared log records without storing their contents.
type InputLogAttempt struct {
	ID, SourcePath, Kind, Outcome string
	StartedAt                     time.Time
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
	if _, err := r.db.ExecContext(ctx, `INSERT INTO input_log_attempts (id, source_path, requested_path, kind, started_at)
		VALUES (?, ?, ?, ?, ?)`, a.ID, a.SourcePath, a.SourcePath, a.Kind, formatWorkflowStateTime(a.StartedAt)); err != nil {
		return fmt.Errorf("save input log association: %w", err)
	}
	return nil
}

// FinishInputLog records the observed outcome and the verified path, when available.
func (r *SQLiteRepository) FinishInputLog(ctx context.Context, id, path, outcome string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE input_log_attempts SET source_path=coalesce(nullif(?, ''), source_path), outcome=?
		WHERE id=?`, path, outcome, id); err != nil {
		return fmt.Errorf("save input log outcome: %w", err)
	}
	return nil
}

// InputLogs lists attempts for a stored path, or finds one exact attempt by its opaque ID.
func (r *SQLiteRepository) InputLogs(ctx context.Context, source, id string) ([]InputLogAttempt, error) {
	return r.inputLogs(ctx, source, id, false)
}

func (r *SQLiteRepository) inputLogs(ctx context.Context, source, id string, includeRequested bool) ([]InputLogAttempt, error) {
	query := r.historyQuery(ctx)
	filter, args := "id = ?", []any{id}
	if id == "" {
		paths, err := inputLogSourceAliases(ctx, query, source)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(paths)
		if err != nil {
			return nil, fmt.Errorf("encode input log aliases: %w", err)
		}
		filter, args = "source_path IN (SELECT value FROM json_each(?))", []any{string(encoded)}
		if includeRequested {
			filter += " OR requested_path = ?"
			args = append(args, source)
		}
	}
	rows, err := query.QueryContext(ctx, `SELECT id, source_path, kind, outcome, started_at FROM input_log_attempts
		WHERE `+filter+` ORDER BY rtrim(started_at,'Z') DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("db list input logs: %w", err)
	}
	defer rows.Close()
	result := make([]InputLogAttempt, 0)
	for rows.Next() {
		var a InputLogAttempt
		var stamp string
		if err := rows.Scan(&a.ID, &a.SourcePath, &a.Kind, &a.Outcome, &stamp); err != nil {
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

// ReadOnlyInputLogs reads retained associations without bootstrapping an app runtime.
func ReadOnlyInputLogs(ctx context.Context, dbPath, source string) ([]InputLogAttempt, error) {
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
	var available bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='input_log_attempts')`).
		Scan(&available); err != nil {
		return nil, fmt.Errorf("inspect input log schema: %w", err)
	}
	if !available {
		return nil, nil
	}
	repo := &SQLiteRepository{db: conn}
	return repo.inputLogs(ctx, source, "", true)
}

// InputLogSources returns up to 100 recent log-only inputs in recency order,
// excluding equivalent paths already displayed by regular History.
func (r *SQLiteRepository) InputLogSources(ctx context.Context, storedPaths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(storedPaths))
	for _, p := range storedPaths {
		seen[pathing.LexicalPathKey(p)] = struct{}{}
	}
	rows, err := r.historyQuery(ctx).QueryContext(ctx, `SELECT source_path FROM input_log_attempts
		GROUP BY source_path ORDER BY max(rtrim(started_at, 'Z')) DESC, source_path`)
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
	key := pathing.LexicalPathKey(source)
	rows, err := query.QueryContext(ctx, `SELECT DISTINCT source_path FROM input_log_attempts`)
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
		if key != "" && key == pathing.LexicalPathKey(path) {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate input log aliases: %w", err)
	}
	return paths, nil
}
